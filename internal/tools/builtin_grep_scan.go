package tools

// builtin_grep_scan.go — per-file scanners used by grep_codebase.
// grepFileMatches is the no-context fast path; grepFileMatchesWithContext
// runs a sliding before-buffer + deferred open-block list so each match
// emits a "before/match/after" block in ripgrep's `:` / `-` separator
// style without re-reading the file. Companion siblings:
//
//   - builtin_grep.go         GrepCodebaseTool + Execute + walker +
//                             splitGlobList + anyGlobMatches +
//                             formatGrepBlock + matchHit + constants
//   - builtin_grep_helpers.go formatGrepRegexError + grepRE2Hint +
//                             isLikelyCatastrophic +
//                             gitignoreMatcher + matchDir/matchFile

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func grepFileMatches(path, rel string, re *regexp.Regexp, beforeLines, afterLines, remaining int) ([]matchHit, []string, bool, error) {
	if remaining <= 0 {
		return nil, nil, true, nil
	}
	if beforeLines > 0 || afterLines > 0 {
		return grepFileMatchesWithContext(path, rel, re, beforeLines, afterLines, remaining)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	bufSize := defaultGrepScannerBufSize
	if bufSize > maxGrepFileSize {
		bufSize = maxGrepFileSize
	}
	scanner.Buffer(make([]byte, 0, bufSize), maxGrepFileSize)

	lines := make([]string, 0, 128)
	lineNo := 0
	hits := make([]matchHit, 0, 4)
	blocks := make([]string, 0, 4)
	perFileMatches := 0
	truncated := false

	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		lines = append(lines, line)
		if !re.MatchString(line) {
			continue
		}
		hits = append(hits, matchHit{Rel: rel, Line: lineNo, Text: strings.TrimSpace(line)})
		if beforeLines > 0 || afterLines > 0 {
			blocks = append(blocks, formatGrepBlock(rel, lines, len(lines)-1, beforeLines, afterLines))
		}
		perFileMatches++
		if len(hits) >= remaining || perFileMatches >= maxGrepMatchesPerFile {
			truncated = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, false, err
	}
	return hits, blocks, truncated, nil
}

func grepFileMatchesWithContext(path, rel string, re *regexp.Regexp, beforeLines, afterLines, remaining int) ([]matchHit, []string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = f.Close() }()

	type grepContextLine struct {
		Number int
		Text   string
	}
	type grepOpenBlock struct {
		Lines          []grepContextLine
		MatchLineIndex int
		RemainingAfter int
	}

	scanner := bufio.NewScanner(f)
	bufSize := defaultGrepScannerBufSize
	if bufSize > maxGrepFileSize {
		bufSize = maxGrepFileSize
	}
	scanner.Buffer(make([]byte, 0, bufSize), maxGrepFileSize)

	formatBlock := func(lines []grepContextLine, matchLineIndex int) string {
		var b strings.Builder
		for i, line := range lines {
			sep := "-"
			if i == matchLineIndex {
				sep = ":"
			}
			fmt.Fprintf(&b, "%s%s%d%s%s", rel, sep, line.Number, sep, strings.TrimRight(line.Text, "\r"))
			if i != len(lines)-1 {
				b.WriteByte('\n')
			}
		}
		return b.String()
	}

	beforeBuf := make([]grepContextLine, 0, beforeLines)
	openBlocks := make([]*grepOpenBlock, 0, afterLines+1)
	hits := make([]matchHit, 0, 4)
	blocks := make([]string, 0, 4)
	perFileMatches := 0
	lineNo := 0
	truncated := false

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		if len(openBlocks) > 0 {
			active := openBlocks[:0]
			for _, block := range openBlocks {
				block.Lines = append(block.Lines, grepContextLine{Number: lineNo, Text: line})
				block.RemainingAfter--
				if block.RemainingAfter <= 0 {
					blocks = append(blocks, formatBlock(block.Lines, block.MatchLineIndex))
					continue
				}
				active = append(active, block)
			}
			openBlocks = active
		}

		if !re.MatchString(line) {
			if beforeLines > 0 {
				beforeBuf = append(beforeBuf, grepContextLine{Number: lineNo, Text: line})
				if len(beforeBuf) > beforeLines {
					beforeBuf = beforeBuf[1:]
				}
			}
			continue
		}
		hits = append(hits, matchHit{Rel: rel, Line: lineNo, Text: strings.TrimSpace(strings.TrimRight(line, "\r"))})
		window := make([]grepContextLine, 0, len(beforeBuf)+1)
		window = append(window, beforeBuf...)
		window = append(window, grepContextLine{Number: lineNo, Text: line})
		if afterLines > 0 {
			openBlocks = append(openBlocks, &grepOpenBlock{
				Lines:          window,
				MatchLineIndex: len(window) - 1,
				RemainingAfter: afterLines,
			})
		} else {
			blocks = append(blocks, formatBlock(window, len(window)-1))
		}
		perFileMatches++
		if len(hits) >= remaining || perFileMatches >= maxGrepMatchesPerFile {
			truncated = true
			break
		}
		if beforeLines > 0 {
			beforeBuf = append(beforeBuf, grepContextLine{Number: lineNo, Text: line})
			if len(beforeBuf) > beforeLines {
				beforeBuf = beforeBuf[1:]
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, false, err
	}
	for _, block := range openBlocks {
		blocks = append(blocks, formatBlock(block.Lines, block.MatchLineIndex))
	}
	return hits, blocks, truncated, nil
}
