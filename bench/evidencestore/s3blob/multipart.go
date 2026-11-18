package s3blob

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

// abortTimeout bounds the best-effort AbortMultipartUpload, which runs even
// when the caller's context is already cancelled.
const abortTimeout = 30 * time.Second

// completedPart is one <Part> of a CompleteMultipartUpload body.
type completedPart struct {
	PartNumber     int    `xml:"PartNumber"`
	ETag           string `xml:"ETag"`
	ChecksumSHA256 string `xml:"ChecksumSHA256,omitempty"`
}

type completeUpload struct {
	XMLName xml.Name        `xml:"CompleteMultipartUpload"`
	Xmlns   string          `xml:"xmlns,attr"`
	Parts   []completedPart `xml:"Part"`
}

// putMultipart uploads a large object in parts: CreateMultipartUpload with
// the sha256 metadata, one UploadPart per section (each with its own
// Content-MD5 and signed sha256, re-read from the ReaderAt on retry),
// CompleteMultipartUpload with the parts in order, then a Head to confirm
// the stored size and sha256. Any failure after the upload starts aborts
// it, best effort, so no parts are left billed in the bucket.
func (c *Client) putMultipart(ctx context.Context, key string, in evidencestore.PutInput) error {
	partSize := c.partSizeFor(in.Size)
	sum, err := hashBody(in.Body, in.Size, partSize)
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	if got := hex.EncodeToString(sum.whole); got != in.SHA256 {
		return fmt.Errorf("s3 put %s: body sha256 %s, want %s: %w", key, got, in.SHA256, evidencestore.ErrChecksum)
	}
	uploadID, err := c.createUpload(ctx, key, in)
	if err != nil {
		return fmt.Errorf("s3 put %s: create multipart upload: %w", key, err)
	}
	parts, err := c.uploadParts(ctx, key, uploadID, in, partSize, sum.parts)
	if err == nil {
		err = c.completeUpload(ctx, key, uploadID, parts, in)
	}
	if err != nil {
		if aerr := c.abortUpload(ctx, key, uploadID); aerr != nil {
			return fmt.Errorf("s3 put %s: %w (abort multipart upload: %v)", key, err, aerr)
		}
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	info, err := c.Head(ctx, key)
	if err != nil {
		return fmt.Errorf("s3 put %s: verify: %w", key, err)
	}
	if info.Size != in.Size {
		return fmt.Errorf("s3 put %s: stored %d bytes, sent %d: %w", key, info.Size, in.Size, evidencestore.ErrChecksum)
	}
	if info.SHA256 != "" && info.SHA256 != in.SHA256 {
		return fmt.Errorf("s3 put %s: stored sha256 %s, sent %s: %w", key, info.SHA256, in.SHA256, evidencestore.ErrChecksum)
	}
	return nil
}

// partSizeFor grows the configured part size, in whole MiB, when the object
// would otherwise need more than the 10000 parts S3 allows.
func (c *Client) partSizeFor(size int64) int64 {
	ps := c.cfg.PartSize
	if need := (size + maxParts - 1) / maxParts; need > ps {
		ps = (need + (1 << 20) - 1) &^ ((1 << 20) - 1)
	}
	return ps
}

// createUpload starts the upload and returns its id. The sha256 metadata
// goes here because S3 copies it onto the completed object.
func (c *Client) createUpload(ctx context.Context, key string, in evidencestore.PutInput) (string, error) {
	h := http.Header{}
	h.Set("Content-Type", contentType(in.ContentType))
	h.Set(metaSHA256, in.SHA256)
	if c.cfg.ChecksumSHA256 {
		h.Set("X-Amz-Checksum-Algorithm", "SHA256")
	}
	r := bytesRequest(http.MethodPost, key, false, url.Values{"uploads": {""}}, h, nil)
	r.errorIn200 = true
	resp, err := c.do(ctx, r)
	if err != nil {
		return "", err
	}
	var res struct {
		UploadID string `xml:"UploadId"`
	}
	if err := decodeXML(resp, &res); err != nil {
		return "", err
	}
	if res.UploadID == "" {
		return "", errors.New("response has no UploadId")
	}
	return res.UploadID, nil
}

// uploadParts sends each part in order and collects the ETags (and
// checksums) that CompleteMultipartUpload needs.
func (c *Client) uploadParts(ctx context.Context, key, uploadID string, in evidencestore.PutInput, partSize int64, sums []partSum) ([]completedPart, error) {
	parts := make([]completedPart, 0, len(sums))
	for i, ps := range sums {
		n := i + 1
		h := http.Header{}
		h.Set("Content-Md5", base64.StdEncoding.EncodeToString(ps.md5))
		cp := completedPart{PartNumber: n}
		if c.cfg.ChecksumSHA256 {
			cp.ChecksumSHA256 = base64.StdEncoding.EncodeToString(ps.sha256)
			h.Set("X-Amz-Checksum-Sha256", cp.ChecksumSHA256)
		}
		resp, err := c.do(ctx, request{
			method:  http.MethodPut,
			key:     key,
			query:   url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {uploadID}},
			header:  h,
			body:    in.Body,
			offset:  int64(i) * partSize,
			size:    ps.size,
			payload: hex.EncodeToString(ps.sha256),
		})
		if err != nil {
			return nil, fmt.Errorf("upload part %d: %w", n, err)
		}
		cp.ETag = resp.Header.Get("Etag")
		if err := closeBody(resp); err != nil {
			return nil, fmt.Errorf("upload part %d: %w", n, err)
		}
		if cp.ETag == "" {
			return nil, fmt.Errorf("upload part %d: response has no ETag", n)
		}
		parts = append(parts, cp)
	}
	return parts, nil
}

// completeUpload finishes the upload. A retried Complete whose first try
// did succeed gets NoSuchUpload; the object is then accepted when Head
// shows the expected size and sha256.
func (c *Client) completeUpload(ctx context.Context, key, uploadID string, parts []completedPart, in evidencestore.PutInput) error {
	body, err := xml.Marshal(completeUpload{Xmlns: s3Namespace, Parts: parts})
	if err != nil {
		return fmt.Errorf("encode complete request: %w", err)
	}
	r := bytesRequest(http.MethodPost, key, false, url.Values{"uploadId": {uploadID}}, http.Header{"Content-Type": {"application/xml"}}, body)
	r.errorIn200 = true
	resp, err := c.do(ctx, r)
	if err != nil {
		var se *Error
		if errors.As(err, &se) && se.Code == "NoSuchUpload" {
			if info, herr := c.Head(ctx, key); herr == nil && info.Size == in.Size && info.SHA256 == in.SHA256 {
				return nil
			}
		}
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	return closeBody(resp)
}

// abortUpload deletes the upload's parts with a fresh deadline, so it still
// runs after the caller's context is cancelled.
func (c *Client) abortUpload(ctx context.Context, key, uploadID string) error {
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()
	resp, err := c.do(actx, request{
		method:  http.MethodDelete,
		key:     key,
		query:   url.Values{"uploadId": {uploadID}},
		payload: emptySHA256,
	})
	if err != nil {
		var se *Error
		if errors.As(err, &se) && se.Code == "NoSuchUpload" {
			return nil
		}
		return err
	}
	return closeBody(resp)
}
