#!/usr/bin/env python3
"""Fix all REPORT.md issues in one shot."""
import os

def fix_file(path, replacements):
    """Read file, apply list of (old, new) replacements, write back."""
    with open(path, 'r', encoding='utf-8') as f:
        content = f.read()
    applied = 0
    for old, new in replacements:
        if old in content:
            content = content.replace(old, new, 1)
            applied += 1
        else:
            print(f"  WARNING: pattern not found in {path}: {old[:80]}...")
    with open(path, 'w', encoding='utf-8') as f:
        f.write(content)
    print(f"  {path}: {applied}/{len(replacements)} replacements applied")

# ── M1: treesitter_cgo.go — truncation indicator ──
fix_file('internal/ast/treesitter_cgo.go', [
    (
        '\tif len(errs) == 0 {\n\t\terrs = append(errs, ParseError{Line: 1, Column: 1, Message: "tree-sitter detected syntax errors"})\n\t}\n\treturn errs',
        '\tif len(errs) >= 8 {\n\t\terrs = append(errs, ParseError{\n\t\t\tLine:    -1,\n\t\t\tColumn:  -1,\n\t\t\tMessage: "...more errors omitted (showing first 8)",\n\t\t})\n\t}\n\tif len(errs) == 0 {\n\t\terrs = append(errs, ParseError{Line: 1, Column: 1, Message: "tree-sitter detected syntax errors"})\n\t}\n\treturn errs'
    ),
])

# ── M2: server.go — security headers middleware ──
fix_file('ui/web/server.go', [
    (
        'func New(eng *engine.Engine, host string, port int) *Server {',
        '// securityHeaders adds browser-enforced security boundaries to every\n'
        '// response. The embedded workbench is self-contained, so we lock down\n'
        '// CSP to \'self\' only and set standard hardening headers.\n'
        'func securityHeaders(h http.Handler) http.Handler {\n'
        '\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {\n'
        '\t\tw.Header().Set("Content-Security-Policy", "default-src \'self\'; script-src \'self\'; style-src \'self\' \'unsafe-inline\'")\n'
        '\t\tw.Header().Set("X-Content-Type-Options", "nosniff")\n'
        '\t\tw.Header().Set("X-Frame-Options", "DENY")\n'
        '\t\th.ServeHTTP(w, r)\n'
        '\t})\n'
        '}\n'
        '\n'
        'func New(eng *engine.Engine, host string, port int) *Server {'
    ),
    (
        'return &http.Server{Addr: s.addr, Handler: s.mux}, nil',
        'return &http.Server{Addr: s.addr, Handler: securityHeaders(s.mux)}, nil'
    ),
])

# ── M3: conversation/manager.go — save-serializing mutex ──
fix_file('internal/conversation/manager.go', [
    (
        'type Manager struct {\n\tmu      sync.RWMutex\n\tstore   *storage.Store\n\tactive  *Conversation\n\tbaseDir string\n}',
        'type Manager struct {\n\tmu      sync.RWMutex\n\tsaveMu  sync.Mutex // serializes saves so snapshots are never stale\n\tstore   *storage.Store\n\tactive  *Conversation\n\tbaseDir string\n}'
    ),
    (
        '\tm.mu.RLock()\n\tif m.active == nil || m.store == nil {',
        '\t// saveMu serializes concurrent SaveActive calls so the snapshot taken\n'
        '\t// by one is not invalidated by another goroutine\'s AddMessage between\n'
        '\t// RUnlock and the disk write. The original comment about not holding\n'
        '\t// m.mu across I/O still applies \xe2\x80\x94 we keep the RLock short.\n'
        '\tm.saveMu.Lock()\n'
        '\tdefer m.saveMu.Unlock()\n'
        '\n'
        '\tm.mu.RLock()\n'
        '\tif m.active == nil || m.store == nil {'
    ),
])

# ── M4: engine.go — log hook timeout ──
fix_file('internal/engine/engine.go', [
    (
        'e.Hooks.Fire(ctx, hooks.EventSessionEnd, hooks.Payload{\n'
        '\t\t\t"project_root": e.ProjectRoot,\n'
        '\t\t})\n'
        '\t}',
        'e.Hooks.Fire(ctx, hooks.EventSessionEnd, hooks.Payload{\n'
        '\t\t\t"project_root": e.ProjectRoot,\n'
        '\t\t})\n'
        '\t\tif ctx.Err() == context.DeadlineExceeded {\n'
        '\t\t\tfmt.Fprintf(os.Stderr, "dfmc: warning: session_end hook timed out after 5s\\n")\n'
        '\t\t}\n'
        '\t}'
    ),
])

# Check if "os" is imported in engine.go
with open('internal/engine/engine.go', 'r', encoding='utf-8') as f:
    econtent = f.read()
if '"os"' not in econtent:
    econtent = econtent.replace('"fmt"', '"fmt"\n\t"os"', 1)
    with open('internal/engine/engine.go', 'w', encoding='utf-8') as f:
        f.write(econtent)
    print('  engine.go: added "os" import')

# ── L3: graph.go — BFS nil-clear for GC ──
fix_file('internal/codemap/graph.go', [
    (
        '\tfor len(queue) > 0 {\n\t\tcur := queue[0]\n\t\tqueue = queue[1:]',
        '\tfor len(queue) > 0 {\n\t\tcur := queue[0]\n\t\tqueue[0] = item{} // clear ref for GC\n\t\tqueue = queue[1:]'
    ),
])

# ── H2: hooks.go — config permission warning ──
# Add a CheckConfigPermissions function after sanitizeEnvKey
fix_file('internal/hooks/hooks.go', [
    (
        '// conditionMatches implements the tiny condition grammar:',
        '// CheckConfigPermissions warns if the DFMC config file is group or\n'
        '// world-writable, which would allow an attacker who can write to the\n'
        '// config to achieve arbitrary code execution via hook commands.\n'
        'func CheckConfigPermissions(configPath string) string {\n'
        '\tinfo, err := os.Stat(configPath)\n'
        '\tif err != nil {\n'
        '\t\treturn ""\n'
        '\t}\n'
        '\tmode := info.Mode().Perm()\n'
        '\tif mode&0020 != 0 || mode&0002 != 0 {\n'
        '\t\treturn fmt.Sprintf("warning: %s is group/world-writable (mode %03o); "+\n'
        '\t\t\t"hook commands run with full shell interpretation and should be treated as trusted", configPath, mode)\n'
        '\t}\n'
        '\treturn ""\n'
        '}\n'
        '\n'
        '// conditionMatches implements the tiny condition grammar:'
    ),
])

# Check if "os" and "fmt" are imported in hooks.go
with open('internal/hooks/hooks.go', 'r', encoding='utf-8') as f:
    hcontent = f.read()
imports_to_add = []
if '"os"' not in hcontent:
    imports_to_add.append('"os"')
if '"fmt"' not in hcontent:
    imports_to_add.append('"fmt"')
if imports_to_add:
    # Find the last import and add after it
    for imp in imports_to_add:
        hcontent = hcontent.replace('"runtime"', '"runtime"\n\t"' + imp.strip('"') + '"', 1) if '"runtime"' in hcontent else hcontent
    with open('internal/hooks/hooks.go', 'w', encoding='utf-8') as f:
        f.write(hcontent)
    print(f'  hooks.go: added imports: {imports_to_add}')

# ── L1: context_panel.go — nil engine guard ──
# Read the file and add nil guard before eng.ContextBudgetPreview call
with open('ui/tui/context_panel.go', 'r', encoding='utf-8') as f:
    cp = f.read()

if 'm.eng != nil' not in cp.split('ContextBudgetPreview')[0][-200:]:
    # Add nil guard around the ContextBudgetPreview call
    old_cp = 'm.eng.ContextBudgetPreview'
    # We wrap the call with a nil check
    cp = cp.replace(
        'm.eng.ContextBudgetPreview',
        'func() *engine.ContextBudgetInfo { if m.eng == nil { return nil }; return m.eng.ContextBudgetPreview }()'
    )
    with open('ui/tui/context_panel.go', 'w', encoding='utf-8') as f:
        f.write(cp)
    print('  context_panel.go: added nil engine guard (L1)')
else:
    print('  context_panel.go: nil guard already present (L1)')

print('\nAll fixes applied!')
