package imvault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"time"
)

type ownershipEntry struct {
	file    File
	expires time.Time
}

// CachedFile remembers successful ownership checks for 15 seconds, scoped to
// the exact credential. Changing accounts cannot inherit the old account's
// grants. Failed checks are never cached. The cache is bounded independently
// of how many image IDs a visitor asks for.
func (c *Client) CachedFile(ctx context.Context, token, id string) (File, error) {
	key := sha256.Sum256([]byte(token + "\x00" + id))
	c.cacheMu.Lock()
	entry, ok := c.ownership[key]
	c.cacheMu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.file, nil
	}
	file, err := c.File(ctx, token, id)
	if err != nil {
		return File{}, err
	}
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.ownership == nil || len(c.ownership) >= 1024 {
		c.ownership = make(map[[32]byte]ownershipEntry)
	}
	c.ownership[key] = ownershipEntry{file, time.Now().Add(15 * time.Second)}
	return file, nil
}

type imageStream struct {
	io.Reader
	body io.Closer
}

func (s *imageStream) Close() error { return s.body.Close() }

type boundedImage struct {
	source io.Reader
	left   int64
}

func (b *boundedImage) Read(p []byte) (int, error) {
	if b.left == 0 {
		var extra [1]byte
		n, err := b.source.Read(extra[:])
		if n > 0 {
			return 0, ErrUnavailable
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.source.Read(p)
	b.left -= int64(n)
	return n, err
}

// StreamImage validates a small prefix, then streams a bounded upstream body.
// Content-Type alone is not evidence that an upstream response is an image.
func (c *Client) StreamImage(ctx context.Context, token, id, rendition string) (io.ReadCloser, string, error) {
	if !IDPattern.MatchString(id) || rendition != "preview" && rendition != "thumb" {
		return nil, "", ErrUnavailable
	}
	resp, err := c.request(ctx, "GET", "/f/"+id+"/"+rendition, token, "", nil)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != 200 || resp.ContentLength > MaxImageBytes {
		closeBody(resp)
		return nil, "", ErrUnavailable
	}
	prefix := make([]byte, 512)
	n, err := io.ReadFull(resp.Body, prefix)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		closeBody(resp)
		return nil, "", ErrUnavailable
	}
	prefix = prefix[:n]
	mime := ImageMIME(prefix)
	if mime == "" {
		closeBody(resp)
		return nil, "", ErrUnavailable
	}
	reader := &boundedImage{source: io.MultiReader(bytes.NewReader(prefix), resp.Body), left: MaxImageBytes}
	return &imageStream{reader, resp.Body}, mime, nil
}
