package calibration

// X1 vision grading (docs/visual-grading.md): calibration items that label
// an image instead of an answer text, for the vision judge. The image is
// kept content-addressed beside the labels, at
// calibration/images/<sha256>.png, and the item names it by its sha256 in
// CalibrationItem.Image. An item without an image is exactly as before:
// same fields, same id.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ImagesDir holds the labelled images, under the calibration folder.
const ImagesDir = "images"

// MaxImageBytes caps one labelled image.
const MaxImageBytes = 16 << 20

var imageSHARe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ItemIDWithImage is the content address of an item: ItemID(task, answer)
// when image is empty, so every existing id stays the same, and otherwise
// the first 16 hex of sha256(task NUL answer NUL "image:" image).
func ItemIDWithImage(task, answer, image string) string {
	if image == "" {
		return ItemID(task, answer)
	}
	return utils.SHA256Bytes([]byte(task + "\x00" + answer + "\x00image:" + image))[:16]
}

// ItemIDOf is the content address of it.
func ItemIDOf(it schemas.CalibrationItem) string {
	return ItemIDWithImage(it.Task, it.Answer, it.Image)
}

// ImagePath is where the labelled image with sha256 sum lives in the suite
// at root.
func ImagePath(root, sum string) string { return filepath.Join(root, Dir, ImagesDir, sum+".png") }

// ValidateImages checks every item that names an image: the file
// calibration/images/<sha256>.png exists, is a regular file of at most
// MaxImageBytes, and its sha256 is the one the item names. Errors wrap
// ErrInput.
func ValidateImages(root string, items []schemas.CalibrationItem) error {
	checked := map[string]bool{}
	for _, it := range items {
		if it.Image == "" || checked[it.Image] {
			continue
		}
		if err := ValidateImage(root, it.Image); err != nil {
			return fmt.Errorf("item %s: %w", it.ID, err)
		}
		checked[it.Image] = true
	}
	return nil
}

// ValidateImage checks one labelled image file.
func ValidateImage(root, sum string) error {
	if !imageSHARe.MatchString(sum) {
		return fmt.Errorf("image %q is not a lowercase sha256: %w", sum, ErrInput)
	}
	p := ImagePath(root, sum)
	st, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("image %s.png is not in %s/%s: %w", sum, Dir, ImagesDir, ErrInput)
	case err != nil:
		return fmt.Errorf("image %s: %w", sum, err)
	case !st.Mode().IsRegular():
		return fmt.Errorf("image %s.png is not a regular file: %w", sum, ErrInput)
	case st.Size() > MaxImageBytes:
		return fmt.Errorf("image %s.png is %d bytes, over the %d byte cap: %w", sum, st.Size(), MaxImageBytes, ErrInput)
	}
	f, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("image %s: %w", sum, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxImageBytes+1)); err != nil {
		return fmt.Errorf("image %s: %w", sum, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("image %s.png has sha256 %s: %w", sum, got, ErrInput)
	}
	return nil
}

// AddImage stores a labelled image under its sha256 (content-addressed, so
// adding the same image twice is one file) and returns the sum.
func AddImage(root string, data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return "", fmt.Errorf("image is %d bytes; want 1 to %d: %w", len(data), MaxImageBytes, ErrInput)
	}
	sum := utils.SHA256Bytes(data)
	p := ImagePath(root, sum)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := ValidateImage(root, sum); err == nil {
		return sum, nil
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return sum, os.Rename(tmp, p)
}
