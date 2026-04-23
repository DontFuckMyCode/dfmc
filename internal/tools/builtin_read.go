// read_file tool: text-file reader with a default 200-line window,
// NUL-byte binary-file guard on the first 512 bytes, UTF-16 BOM decode,
// and an extension -> language label so the model gets a
// syntax-highlight hint. Extracted from builtin.go.

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

type ReadFileTool struct{}

const readFileBinaryCheckBytes = 512

const (
	readFileEncodingUTF8       = "utf-8"
	readFileEncodingUTF16LEBOM = "utf-16le-bom"
	readFileEncodingUTF16BEBOM = "utf-16be-bom"
)

func NewReadFileTool() *ReadFileTool { return &ReadFileTool{} }
func (t *ReadFileTool) Name() string { return "read_file" }
func (t *ReadFileTool) Description() string {
	return "Read a text file with optional line range."
}
func (t *ReadFileTool) Execute(_ context.Context, req Request) (Result, error) {
	path := asString(req.Params, "path", "")
	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return Result{}, err
	}
	const maxFileSize = 10 << 20 // 10 MB
	if info.Size() > maxFileSize {
		return Result{}, fmt.Errorf("file too large (%d bytes, limit %d) \u2014 use line_start/line_end to read a segment", info.Size(), maxFileSize)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{}, err
	}

	text, encoding, checkLen, err := decodeReadFileText(data)
	if err != nil {
		return Result{}, err
	}

	// Reject binary files using a cheap text heuristic: if the first 512
	// bytes contain a NUL, this is almost certainly not text. We surface the
	// window size in Result.Data for successful reads too, so callers know
	// exactly what "binary-safe" means here.
	lines := strings.Split(text, "\n")

	start := asInt(req.Params, "line_start", 1)
	// Default end caps the window at 200 lines from `start` so a bare
	// {"path":"X"} call doesn't dump a 5000-line file into the model's
	// context. The Spec advertises this default and the spec's `view`
	// contract relies on `truncated:true` firing when the cap kicks in;
	// without the cap, truncated was dead code. Callers that genuinely
	// want the whole file can pass line_end explicitly.
	const defaultWindow = 200
	defaultEnd := start + defaultWindow - 1
	if defaultEnd > len(lines) {
		defaultEnd = len(lines)
	}
	end := asInt(req.Params, "line_end", defaultEnd)
	if start < 1 {
		start = 1
	}
	if start > len(lines)+1 {
		start = len(lines) + 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start-1 {
		end = start - 1
	}

	segment := strings.Join(lines[start-1:end], "\n")
	// Returned-vs-total telemetry — mirrors the spec's `view` contract:
	// the model gets `total_lines` so it can decide whether to widen the
	// range, and `truncated` so a partial read is loud, not silent.
	totalLines := len(lines)
	returnedLines := end - start + 1
	if returnedLines < 0 {
		returnedLines = 0
	}
	// truncated means "the file extends past what you got" — always set
	// when returned < total. We can't reliably distinguish "caller asked
	// for a slice" from "engine clamped to default 200" here because the
	// engine's normalizeToolParams injects default line_start/line_end
	// before Execute runs, so the caller-intent signal is gone by this
	// point. Honest answer: tell the model whether it got everything.
	truncated := returnedLines < totalLines
	if truncated {
		segment = appendReadFileTruncationMarker(segment, start, end, totalLines)
	}
	contentSum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(contentSum[:])
	return Result{
		Output: segment,
		Data: map[string]any{
			"path":               PathRelativeToRoot(req.ProjectRoot, absPath),
			"content_sha256":     contentHash,
			"line_start":         start,
			"line_end":           end,
			"line_count":         totalLines, // legacy field name
			"total_lines":        totalLines, // spec-aligned alias
			"returned_lines":     returnedLines,
			"truncated":          truncated,
			"language":           detectLanguageFromExt(absPath),
			"encoding":           encoding,
			"binary_check_bytes": checkLen,
			"binary_heuristic":   "nul-in-first-window",
		},
		Truncated: truncated,
	}, nil
}

func decodeReadFileText(data []byte) (string, string, int, error) {
	checkLen := len(data)
	if checkLen > readFileBinaryCheckBytes {
		checkLen = readFileBinaryCheckBytes
	}
	if len(data) >= 2 {
		switch {
		case data[0] == 0xFF && data[1] == 0xFE:
			text, err := decodeUTF16WithBOM(data[2:], binary.LittleEndian)
			return text, readFileEncodingUTF16LEBOM, checkLen, err
		case data[0] == 0xFE && data[1] == 0xFF:
			text, err := decodeUTF16WithBOM(data[2:], binary.BigEndian)
			return text, readFileEncodingUTF16BEBOM, checkLen, err
		}
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return "", "", checkLen, fmt.Errorf("file appears to be binary (NUL byte at offset %d) \u2014 read_file only supports text files", i)
		}
	}
	return string(data), readFileEncodingUTF8, checkLen, nil
}

func decodeUTF16WithBOM(data []byte, order binary.ByteOrder) (string, error) {
	if len(data)%2 != 0 {
		return "", fmt.Errorf("file appears to be malformed UTF-16 text (odd payload length after BOM)")
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		units = append(units, order.Uint16(data[i:i+2]))
	}
	return string(utf16.Decode(units)), nil
}

// detectLanguageFromExt maps a file path to a short language tag the
// model can use for syntax-highlighting hints. Mirrors the AST engine's
// extension table; centralised here as a small lookup so read_file
// doesn't pull in the full AST stack just for a string label.
func detectLanguageFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala":
		return "scala"
	case ".php":
		return "php"
	case ".rb":
		return "ruby"
	case ".lua":
		return "lua"
	case ".md", ".markdown":
		return "markdown"
	case ".yml", ".yaml":
		return "yaml"
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".sh", ".bash":
		return "bash"
	}
	return ""
}

func appendReadFileTruncationMarker(segment string, start, end, totalLines int) string {
	if end < start {
		return segment
	}
	beforeOmitted := start - 1
	afterOmitted := totalLines - end
	if afterOmitted < 0 {
		afterOmitted = 0
	}
	if beforeOmitted < 0 {
		beforeOmitted = 0
	}
	msg := ""
	switch {
	case beforeOmitted > 0 && afterOmitted > 0:
		msg = fmt.Sprintf("... [truncated - %d lines omitted before, %d after]", beforeOmitted, afterOmitted)
	case beforeOmitted > 0:
		msg = fmt.Sprintf("... [truncated - %d lines omitted before]", beforeOmitted)
	case afterOmitted > 0:
		msg = fmt.Sprintf("... [truncated - %d more lines omitted]", afterOmitted)
	default:
		return segment
	}
	if strings.TrimSpace(segment) == "" {
		return msg
	}
	return strings.TrimRight(segment, "\n") + "\n" + msg
}
