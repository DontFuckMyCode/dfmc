# SC-Deserialization Security Scan Results

**Date:** 2026-04-30  
**Project:** DFMC  
**Total Findings:** 0  

## Summary

No unsafe deserialization vulnerabilities detected in DFMC.

### Verification Findings

#### Serialization Format 1: YAML Configuration
**File:** `internal/config/config.go:127-143`  
**Status:** SAFE ✓

```go
func loadYAML(path string, out *Config) error {
    data, err := os.ReadFile(path)
    if err != nil {
        return err
    }
    if len(data) == 0 {
        return nil
    }
    const maxConfigSize = 1 << 20 // 1 MB — prevents memory exhaustion
    if len(data) > maxConfigSize {
        return fmt.Errorf("%s is %d bytes (max 1 MB); refusing to parse", path, len(data))
    }
    if err := yaml.Unmarshal(data, out); err != nil {
        return fmt.Errorf("parse %s: %w", path, err)
    }
    return nil
}
```

**Verification:**
- **Source:** User-controlled local file (`.dfmc/config.yaml`, global `~/.dfmc/config.yaml`)
- **YAML parser:** `gopkg.in/yaml.v3` with default settings
- **Type safety:** YAML unmarshals only into predefined `Config` struct; no arbitrary object instantiation
- **Size cap:** 1 MB prevents billion-laughs or compression-bomb attacks
- **Unsafe types (anchors, merges):** YAML v3 defaults are safe; no custom unmarshaler registration

**Risk assessment:** YAML deserialization of a trusted local config file is low-risk. The operator who can write the config can already execute arbitrary code via hooks/plugins.

#### Serialization Format 2: JSON
**File:** Multiple (e.g., `internal/conversation/manager.go`, `ui/web/server_ws.go`)  
**Status:** SAFE ✓

JSON is used throughout DFMC for:
- Conversation persistence (`.json` JSONL format)
- WebSocket message dispatch (`json.Unmarshal()`)
- HTTP request/response bodies (`json.NewDecoder()`, `json.Unmarshal()`)

**Verification:**
- **Parser:** Go's `encoding/json` (safe; only literal values, no code execution)
- **No custom marshalers** that could execute code
- **Type-safe:** Unmarshals only into predefined structs (e.g., `types.Message`, `wsMessage`)

#### Serialization Format 3: Protocol Buffers (if used)
**Status:** NOT FOUND

`grep -r "protobuf\|proto\\.UnmarshalMerge"` returned no results. DFMC does not use Protocol Buffers.

#### Serialization Format 4: Binary Formats (gob, binary, etc.)
**Status:** NOT FOUND

`grep -r "encoding/gob\|binary\\.Unmarshal"` returned no results. DFMC does not use Go's `gob` package or custom binary serialization.

#### Serialization Format 5: BBolt Key-Value Store
**File:** `internal/storage/` (used by drive, conversation store)  
**Status:** SAFE ✓

BBolt stores JSON-encoded structs. Key-value blobs are:
- Always JSON (`encoding/json`, which is safe)
- Unmarshaled into predefined structs only
- Never used with unsafe formats or custom unmarshalers

No evidence of `gob` or pickle-like formats stored in BBolt.

#### Serialization Format 6: Conversation Persistence
**File:** `internal/conversation/manager.go`  
**Status:** SAFE ✓ (Recently Fixed)

Conversation JSONL loader was recently fixed:
- **Issue:** 8 MB scan buffer cap (line 82-83 in persistence.go context) prevents malicious JSON from exhausting memory
- **Format:** Standard JSON, no unsafe types
- **Type safety:** Unmarshals into `persistedConversation` struct (line 65-73 in manager.go)

### False Positives Cleared

- `json.Unmarshal()` calls are all safe (JSON cannot instantiate arbitrary code)
- `yaml.Unmarshal()` only on trusted local config file with 1 MB size cap
- No pickle, `ObjectInputStream`, `BinaryFormatter`, Ruby `Marshal`, or other dangerous formats

## Conclusion

**Risk Level:** LOW  
DFMC uses only safe serialization formats (JSON, YAML-to-struct). No unsafe deserialization gadget chains are possible. Configuration YAML is trusted and size-capped.

