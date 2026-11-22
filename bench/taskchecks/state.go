package taskchecks

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite" // read-only queries on SQLite state exports
)

// Limits of state reads.
const (
	maxStateJSON = 64 << 20
	maxSQLRows   = 10000
	sqlTimeout   = 10 * time.Second
)

// DirState reads exports from a run folder's state directory
// (state/<service>/<export id>). Decoded JSON is kept for the next check.
type DirState struct {
	Root  string
	cache map[string]any
}

// NewDirState reads exports under root.
func NewDirState(root string) *DirState { return &DirState{Root: root, cache: map[string]any{}} }

// path resolves file under Root, refusing any path that leaves it.
func (d *DirState) path(file string) (string, error) {
	if !stateFileRE.MatchString(file) || strings.Contains(file, "..") {
		return "", fmt.Errorf("state file %q is not <service>/<export id>", file)
	}
	p := filepath.Join(d.Root, filepath.FromSlash(file))
	st, err := os.Lstat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("not exported (the service has no export %s, or the export failed)", file)
	case err != nil:
		return "", err
	case !st.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file", file)
	}
	return p, nil
}

// JSON implements StateReader.
func (d *DirState) JSON(file string) (any, error) {
	if v, ok := d.cache[file]; ok {
		return v, nil
	}
	p, err := d.path(file)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxStateJSON+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStateJSON {
		return nil, fmt.Errorf("over %d MiB", maxStateJSON>>20)
	}
	if bytes.HasPrefix(b, []byte("SQLite format 3\x00")) {
		return nil, errors.New("a SQLite database: test it with sql, not path")
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not JSON: %v", err)
	}
	d.cache[file] = v
	return v, nil
}

// Query implements StateReader: the export is opened read-only and
// immutable, so a query can never change it.
func (d *DirState) Query(file, q string) ([][]any, error) {
	if err := checkSQL(q); err != nil {
		return nil, err
	}
	p, err := d.path(file)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 16)
	if f, err := os.Open(p); err == nil {
		_, _ = io.ReadFull(f, head)
		f.Close()
	}
	if !bytes.Equal(head, []byte("SQLite format 3\x00")) {
		return nil, errors.New("not a SQLite database: test a JSON export with path")
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: p}).EscapedPath()+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), sqlTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]any
	for rows.Next() {
		if len(out) == maxSQLRows {
			return nil, fmt.Errorf("query returned more than %d rows; count them in SQL", maxSQLRows)
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

var sqlStartRE = regexp.MustCompile(`(?i)^\s*(select|with)\b`)

// sqlWriteRE are statements a state check never runs (the database is
// opened read-only as well; this refuses them before it is opened).
var sqlWriteRE = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|attach|detach|pragma|vacuum|replace|reindex)\b`)

// checkSQL accepts one SELECT (or WITH ... SELECT) statement.
func checkSQL(q string) error {
	t := strings.TrimSpace(q)
	t = strings.TrimSuffix(t, ";")
	switch {
	case t == "":
		return errors.New("empty query")
	case !sqlStartRE.MatchString(t):
		return errors.New("a state check runs one SELECT (or WITH ... SELECT)")
	case strings.Contains(t, ";"):
		return errors.New("one statement only (no ;)")
	case sqlWriteRE.MatchString(stripStrings(t)):
		return errors.New("a state check only reads: no insert, update, delete, pragma, attach or schema change")
	}
	return nil
}

var sqlStringRE = regexp.MustCompile(`'(?:[^']|'')*'`)

// stripStrings blanks quoted literals, so a word inside one is not read as
// a statement.
func stripStrings(q string) string { return sqlStringRE.ReplaceAllString(q, "''") }
