// logger_helpers.go — bus subscription reflection bridge, payload
// value coercers, and the atomic file writer used by the logger
// flush path. Sibling of logger.go which keeps the ToolCallRecord
// JSONL shape, Logger struct, Init lifecycle, on-call/on-result
// payload-shape handlers, push/flush/periodicFlush flush orchestration,
// and Close shutdown.
//
// Splitting these out keeps logger.go scoped to "what does a tool
// call look like in the JSONL log and how do we batch+flush it"
// while this file owns "how do we hook the engine event bus without
// importing engine, how do we coerce loosely-typed payload values,
// and how do we write the daily JSONL file atomically." The bus
// bridge uses reflection to avoid an import cycle: engine imports
// toolhistory, so toolhistory cannot import engine.

package toolhistory

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

func subscribePayload(bus any, eventType string, fn func(any)) func() {
	if bus == nil || fn == nil {
		return func() {}
	}
	if typed, ok := bus.(interface {
		SubscribeFunc(string, func(any)) func()
	}); ok {
		return typed.SubscribeFunc(eventType, fn)
	}

	method := reflect.ValueOf(bus).MethodByName("SubscribeFunc")
	if !method.IsValid() {
		return func() {}
	}
	methodType := method.Type()
	if methodType.NumIn() != 2 || methodType.In(0).Kind() != reflect.String || methodType.In(1).Kind() != reflect.Func {
		return func() {}
	}
	callbackType := methodType.In(1)
	if callbackType.NumIn() != 1 || callbackType.NumOut() != 0 {
		return func() {}
	}
	callback := reflect.MakeFunc(callbackType, func(args []reflect.Value) []reflect.Value {
		if len(args) == 0 {
			fn(nil)
			return nil
		}
		fn(payloadFromEventValue(args[0]))
		return nil
	})
	results := method.Call([]reflect.Value{reflect.ValueOf(eventType), callback})
	if len(results) != 1 || results[0].Kind() != reflect.Func {
		return func() {}
	}
	return func() {
		results[0].Call(nil)
	}
}

func payloadFromEventValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		if field := v.FieldByName("Payload"); field.IsValid() && field.CanInterface() {
			return field.Interface()
		}
	}
	if v.CanInterface() {
		return v.Interface()
	}
	return nil
}

func strVal(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func intVal(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func boolVal(m map[string]any, k string) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Result must fit inside max characters total. The ellipsis itself is
	// three chars, so prefixes are sized at max-3. When max is too small
	// to fit even one content char before the ellipsis (max < 4), just
	// surface the ellipsis as the canonical truncation marker — better
	// than silently returning a string longer than the caller's budget.
	if max < 4 {
		return "..."
	}
	return s[:max-3] + "..."
}

// writeFileAtomic is duplicated from internal/storage/store.go to keep
// the toolhistory package free of internal storage imports.
func writeFileAtomic(path string, data []byte, pattern string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	// Delegate to the platform-specific implementation. On Unix this
	// is an os.Open + f.Sync (the POSIX directory-fsync that guarantees
	// the rename in writeFileAtomic is durable). On Windows there is no
	// equivalent API (FlushFileBuffers on a read-only directory handle
	// returns "Access is denied" and the read-write handle requires
	// FILE_FLAG_BACKUP_SEMANTICS plumbing the stdlib doesn't expose) —
	// the Windows variant in logger_helpers_windows.go is a stat-only
	// no-op, which is correct because NTFS's journaling guarantees the
	// rename atomicity that the POSIX dir-fsync exists to enforce.
	return syncDirPlatform(dir)
}
