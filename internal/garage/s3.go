package garage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// S3 is a minimal S3 client for one bucket's key, talking to Garage on
// loopback. It signs requests itself (AWS Signature Version 4), so rig
// needs no third-party module.
type S3 struct {
	Addr    string // host:port, normally S3Addr
	KeyID   string
	Secret  string
	HTTP    *http.Client
	Now     func() time.Time
	MaxKeys int // page size for List (0 = server default); tests use small pages
}

// NewS3 returns a client for the local Garage with the given key.
func NewS3(keyID, secret string) *S3 {
	return &S3{Addr: S3Addr, KeyID: keyID, Secret: secret, HTTP: &http.Client{Timeout: 10 * time.Minute}, Now: time.Now}
}

// Object is one entry of a listing.
type Object struct {
	Key          string    `xml:"Key" json:"key"`
	Size         int64     `xml:"Size" json:"size"`
	LastModified time.Time `xml:"LastModified" json:"last_modified"`
}

// ErrNotFound means the object does not exist.
var ErrNotFound = errors.New("no such file in the bucket")

// CheckKey rejects object names rig won't handle: empty, too long, not
// UTF-8, starting with "/", or containing control characters (which could
// mess up terminal output).
func CheckKey(key string) error {
	switch {
	case key == "" || len(key) > 1024:
		return errors.New("file name must be 1-1024 bytes")
	case !utf8.ValidString(key):
		return errors.New("file name is not valid UTF-8")
	case strings.HasPrefix(key, "/"):
		return errors.New("file name must not start with /")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return errors.New("file name contains control characters")
		}
	}
	return nil
}

// List returns all objects whose name starts with prefix, following
// continuation pages.
func (c *S3) List(bucket, prefix string) ([]Object, error) {
	var all []Object
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		if c.MaxKeys > 0 {
			q.Set("max-keys", strconv.Itoa(c.MaxKeys))
		}
		resp, err := c.do(http.MethodGet, bucket, "", q, nil, -1)
		if err != nil {
			return nil, err
		}
		var page struct {
			Contents              []Object `xml:"Contents"`
			IsTruncated           bool     `xml:"IsTruncated"`
			NextContinuationToken string   `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading listing: %w", err)
		}
		all = append(all, page.Contents...)
		if !page.IsTruncated || page.NextContinuationToken == "" {
			return all, nil
		}
		token = page.NextContinuationToken
	}
}

// Get writes the object's content to w.
func (c *S3) Get(bucket, key string, w io.Writer) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	resp, err := c.do(http.MethodGet, bucket, key, nil, nil, -1)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

// Put uploads size bytes from r as key.
func (c *S3) Put(bucket, key string, r io.Reader, size int64) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	resp, err := c.do(http.MethodPut, bucket, key, nil, r, size)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Delete removes key. S3 reports success even if it didn't exist.
func (c *S3) Delete(bucket, key string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	resp, err := c.do(http.MethodDelete, bucket, key, nil, nil, -1)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// do sends a signed request. The payload is not hashed
// ("UNSIGNED-PAYLOAD"), so large files stream without being read twice;
// the connection is loopback-only, and the signature still covers the
// method, path, query and headers.
func (c *S3) do(method, bucket, key string, q url.Values, body io.Reader, size int64) (*http.Response, error) {
	path := "/" + bucket
	if key != "" {
		path += "/" + key
	}
	encodedPath := uriEncode(path, false)
	query := canonicalQuery(q)
	u := "http://" + c.Addr + encodedPath
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	headers := map[string]string{
		"host":                 c.Addr,
		"x-amz-content-sha256": "UNSIGNED-PAYLOAD",
		"x-amz-date":           c.Now().UTC().Format("20060102T150405Z"),
	}
	auth := signV4(method, encodedPath, query, headers, "UNSIGNED-PAYLOAD", Region, c.KeyID, c.Secret)
	for k, v := range headers {
		if k != "host" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Authorization", auth)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("S3 %s: HTTP %d: %.200s", method, resp.StatusCode, data)
	}
	return resp, nil
}

// signV4 returns the Authorization header for a request, following AWS
// Signature Version 4. headers must use lowercase names and include
// "host" and "x-amz-date"; all of them are signed.
func signV4(method, encodedPath, query string, headers map[string]string, payloadHash, region, keyID, secret string) string {
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{method, encodedPath, query, canonHeaders.String(), signed, payloadHash}, "\n")

	amzDate := headers["x-amz-date"]
	day := amzDate[:8]
	scope := day + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])

	k := hmacSHA256([]byte("AWS4"+secret), day)
	for _, part := range []string{region, "s3", "aws4_request"} {
		k = hmacSHA256(k, part)
	}
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	return fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", keyID, scope, signed, sig)
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// uriEncode percent-encodes s the way AWS signatures require: letters,
// digits and "-_.~" stay; everything else becomes %XX (uppercase). "/"
// stays too unless encodeSlash.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// canonicalQuery encodes query parameters sorted by name, as AWS requires.
func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}
