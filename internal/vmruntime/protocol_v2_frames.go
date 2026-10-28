package vmruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"io"
	"strconv"
	"unicode/utf8"
)

// strictJSONV2 adds UTF-8, surrogate and canonical integer checks without changing
// the v1 decoder. Exact field sets (including nested keys) remain mandatory.
func strictJSONV2(raw []byte, v any) error {
	if !utf8.Valid(raw) || !validEscapesV2(raw) {
		return ErrProtocolV2
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	for {
		token, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return ErrProtocolV2
		}
		if n, ok := token.(json.Number); ok {
			value, e := strconv.ParseInt(string(n), 10, 64)
			if e != nil || strconv.FormatInt(value, 10) != string(n) {
				return ErrProtocolV2
			}
		}
	}
	if strictJSON(raw, v) != nil {
		return ErrProtocolV2
	}
	return nil
}
func validEscapesV2(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// Private failure codes describe only the rejecting branch, never input/error
// text. The public protocol error and exact reader operations remain unchanged.
type protocolFailureV2 struct{ code string }

func (e *protocolFailureV2) Error() string { return ErrProtocolV2.Error() }
func (e *protocolFailureV2) Unwrap() error { return ErrProtocolV2 }
func metadataReadFailureV2(part string, e error) error {
	suffix := "read_failed"
	if e == io.EOF {
		suffix = "eof"
	} else if e == io.ErrUnexpectedEOF {
		suffix = "short"
	}
	return &protocolFailureV2{part + "_" + suffix}
}
func readMetadataV2(r io.Reader, limit int, v any) ([]byte, error) {
	var header [len(HeaderV2) + 4]byte
	if _, e := io.ReadFull(r, header[:]); e != nil {
		return nil, metadataReadFailureV2("header", e)
	}
	if string(header[:len(HeaderV2)]) != HeaderV2 {
		return nil, &protocolFailureV2{"header_invalid"}
	}
	n := binary.BigEndian.Uint32(header[len(HeaderV2):])
	if n == 0 || n > uint32(limit) {
		return nil, &protocolFailureV2{"length_rejected"}
	}
	raw := make([]byte, int(n))
	if _, e := io.ReadFull(r, raw); e != nil {
		return nil, metadataReadFailureV2("body", e)
	}
	if strictJSONV2(raw, v) != nil {
		return nil, &protocolFailureV2{"json_rejected"}
	}
	return raw, nil
}

func writeMetadataV2(w io.Writer, v any, limit int) error {
	raw, e := json.Marshal(v)
	if e != nil || len(raw) == 0 || len(raw) > limit {
		return ErrProtocolV2
	}
	header := binary.BigEndian.AppendUint32([]byte(HeaderV2), uint32(len(raw)))
	if writeBytesV2(w, header) != nil || writeBytesV2(w, raw) != nil {
		return ErrProtocolV2
	}
	return nil
}
func writeBytesV2(w io.Writer, b []byte) error {
	n, e := w.Write(b)
	if e != nil || n != len(b) {
		return ErrProtocolV2
	}
	return nil
}
func writePayloadV2(w io.Writer, kind uint8, index uint32, data []byte) error {
	header := []byte{kind}
	header = binary.BigEndian.AppendUint32(header, index)
	header = binary.BigEndian.AppendUint64(header, uint64(len(data)))
	if writeBytesV2(w, header) != nil {
		return ErrProtocolV2
	}
	for len(data) > 0 {
		n := min(len(data), 32<<10)
		if writeBytesV2(w, data[:n]) != nil {
			return ErrProtocolV2
		}
		data = data[n:]
	}
	return nil
}
func readPayloadHeaderV2(r io.Reader, kind uint8, index uint32, size int64) error {
	var h [13]byte
	if _, e := io.ReadFull(r, h[:]); e != nil || h[0] != kind || binary.BigEndian.Uint32(h[1:]) != index || binary.BigEndian.Uint64(h[5:]) != uint64(size) {
		return ErrProtocolV2
	}
	return nil
}

type contextReaderV2 struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReaderV2) Read(p []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, ErrProtocolV2
	}
	n, e := r.r.Read(p)
	if r.ctx.Err() != nil {
		return n, ErrProtocolV2
	}
	return n, e
}

type contextWriterV2 struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriterV2) Write(p []byte) (int, error) {
	if w.ctx.Err() != nil {
		return 0, ErrProtocolV2
	}
	n, e := w.w.Write(p)
	if w.ctx.Err() != nil {
		return n, ErrProtocolV2
	}
	return n, e
}

// bodyReaderV2 never reads ahead, even if a sink asks for a larger buffer. Sink
// reads are invalid after the callback returns; source validation carries at most
// three incomplete UTF-8 bytes across reads.
type bodyReaderV2 struct {
	r              io.Reader
	remaining      int64
	hash           io.Writer
	text           bool
	tail           []byte
	failed, closed bool
}

func (b *bodyReaderV2) Read(p []byte) (int, error) {
	if b.closed || b.failed {
		return 0, ErrProtocolV2
	}
	if b.remaining == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	n, e := b.r.Read(p[:min(int64(len(p)), b.remaining, 32<<10)])
	if n > 0 {
		b.remaining -= int64(n)
		b.hash.Write(p[:n])
		if b.text && !b.validText(p[:n]) {
			b.failed = true
			return n, ErrProtocolV2
		}
	}
	if e != nil && (e != io.EOF || b.remaining != 0) {
		b.failed = true
		return n, ErrProtocolV2
	}
	return n, e
}
func (b *bodyReaderV2) validText(p []byte) bool {
	if bytes.IndexByte(p, 0) >= 0 {
		return false
	}
	if len(b.tail) > 0 {
		for len(p) > 0 && !utf8.FullRune(b.tail) {
			b.tail = append(b.tail, p[0])
			p = p[1:]
		}
		if !utf8.FullRune(b.tail) {
			return true
		}
		_, n := utf8.DecodeRune(b.tail)
		if n == 1 && b.tail[0] >= utf8.RuneSelf {
			return false
		}
		b.tail = nil
	}
	for len(p) > 0 {
		if !utf8.FullRune(p) {
			b.tail = append([]byte{}, p...)
			return true
		}
		_, n := utf8.DecodeRune(p)
		if n == 1 && p[0] >= utf8.RuneSelf {
			return false
		}
		p = p[n:]
	}
	return true
}

// ReadRequestV2 consumes exactly one complete transaction and performs no
// buffered read-ahead or EOF wait. The caller owns any bufio.Reader and must
// reject already buffered post-terminal bytes and monitor later observed input
// until shutdown. Success alone cannot prove that a serial stream stays quiet.
// Cancellation is checked between reads; the caller must interrupt a blocked
// transport read/write using its native lifecycle deadline/close mechanism.
func ReadRequestV2(ctx context.Context, r io.Reader, sink FrameSinkV2) (WireRequestV2, error) {
	r = contextReaderV2{ctx, r}
	var q WireRequestV2
	raw, e := readMetadataV2(r, MaxRequestBytesV2, &q)
	if e != nil || ValidateRequestV2(q) != nil {
		return WireRequestV2{}, ErrProtocolV2
	}
	if q.Verify != nil {
		var fields struct {
			Verify map[string]json.RawMessage `json:"verify"`
		}
		if json.Unmarshal(raw, &fields) != nil {
			return WireRequestV2{}, ErrProtocolV2
		}
		for key, limit := range map[string]int{"source_files": MaxSourceMetadataBytesV2, "inputs": domain.MaxC4InputContractBytesV2, "dependency_manifest": dependencybundle.MaxManifestBytes} {
			if len(fields.Verify[key]) > limit {
				return WireRequestV2{}, ErrProtocolV2
			}
		}
		if sink == nil {
			return WireRequestV2{}, ErrProtocolV2
		}
		snapshot := sha256.New()
		for group, files := range [][]SourceFileV2{q.Verify.SourceFiles, dependencyFilesV2(q.Verify.DependencyManifest)} {
			for i, f := range files {
				kind := uint8(group + 1)
				if readPayloadHeaderV2(r, kind, uint32(i), f.Size) != nil {
					return WireRequestV2{}, ErrProtocolV2
				}
				h := sha256.New()
				var hashes io.Writer = h
				if kind == 1 {
					fmt.Fprintf(snapshot, "%d:%s:%d:%o:", len(f.Path), f.Path, f.Size, f.Mode)
					hashes = io.MultiWriter(h, snapshot)
				}
				body := &bodyReaderV2{r: r, remaining: f.Size, hash: hashes, text: kind == 1}
				e := sink(kind, uint32(i), f, body)
				body.closed = true
				if e != nil || body.failed || body.remaining != 0 || len(body.tail) != 0 || hex.EncodeToString(h.Sum(nil)) != f.SHA256 || ctx.Err() != nil {
					return WireRequestV2{}, ErrProtocolV2
				}
			}
		}
		if hex.EncodeToString(snapshot.Sum(nil)) != q.Verify.CandidateDigest {
			return WireRequestV2{}, ErrProtocolV2
		}
	}
	if readPayloadHeaderV2(r, 3, 0, 32) != nil {
		return WireRequestV2{}, ErrProtocolV2
	}
	var terminal [32]byte
	if _, e = io.ReadFull(r, terminal[:]); e != nil {
		return WireRequestV2{}, ErrProtocolV2
	}
	want, _ := RequestDigestV2(q)
	if hex.EncodeToString(terminal[:]) != want || ctx.Err() != nil {
		return WireRequestV2{}, ErrProtocolV2
	}
	return q, nil
}

// WriteRequestV2 owns the store transfer and emits the terminal digest only after
// ReadVerified's final payload/manifest/inventory checks succeed. On any failure,
// close the transaction; never retry on the same serial connection.
func WriteRequestV2(ctx context.Context, w io.Writer, q WireRequestV2, source []SnapshotFile, dependencyStoreRoot string) error {
	if ctx.Err() != nil || ValidateRequestV2(q) != nil {
		return ErrProtocolV2
	}
	if q.Action == "probe" {
		if len(source) != 0 || dependencyStoreRoot != "" {
			return ErrProtocolV2
		}
	} else {
		meta, e := SourceMetadataV2(source)
		if e != nil || len(meta) != len(q.Verify.SourceFiles) {
			return ErrProtocolV2
		}
		for i, f := range meta {
			if f != q.Verify.SourceFiles[i] {
				return ErrProtocolV2
			}
		}
		digest, e := SnapshotDigest(source)
		if e != nil || digest != q.Verify.CandidateDigest || dependencyStoreRoot == "" {
			return ErrProtocolV2
		}
	}
	w = contextWriterV2{ctx, w}
	if writeMetadataV2(w, q, MaxRequestBytesV2) != nil {
		return ErrProtocolV2
	}
	if q.Verify != nil {
		for i, f := range source {
			if writePayloadV2(w, 1, uint32(i), f.Data) != nil {
				return ErrProtocolV2
			}
		}
		b, _ := q.Verify.DependencyManifest.Binding()
		index := 0
		e := dependencybundle.ReadVerified(ctx, dependencyStoreRoot, b, func(f dependencybundle.File, data []byte) error {
			if index >= len(q.Verify.DependencyManifest.Files) || f != q.Verify.DependencyManifest.Files[index] {
				return ErrProtocolV2
			}
			if writePayloadV2(w, 2, uint32(index), data) != nil {
				return ErrProtocolV2
			}
			index++
			return nil
		})
		if e != nil || index != len(q.Verify.DependencyManifest.Files) {
			return ErrProtocolV2
		}
	}
	digest, err := RequestDigestV2(q)
	if err != nil {
		return ErrProtocolV2
	}
	terminal, _ := hex.DecodeString(digest)
	return writePayloadV2(w, 3, 0, terminal)
}
