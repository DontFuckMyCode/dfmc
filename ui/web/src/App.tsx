import { KeyboardEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  Bot,
  Boxes,
  BrainCircuit,
  CheckCircle2,
  Code2,
  FileText,
  GitBranch,
  Moon,
  Play,
  RefreshCw,
  Send,
  Sparkles,
  Sun,
  Trash2,
  Workflow,
  Zap,
} from "lucide-react";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { Progress } from "./components/ui/progress";
import { TabsList, TabsTrigger } from "./components/ui/tabs";
import { Textarea } from "./components/ui/textarea";

type AnyRecord = Record<string, any>;
type ChatMessage = { role: "system" | "user" | "assistant"; content: string };
type ActivityEntry = { id: number; ts: string; kind: string; icon: string; text: string };
type DriveRun = AnyRecord;
type Task = AnyRecord;
type ThemeMode = "dark" | "light";

const activityLimit = 500;

function readTokenFromHash() {
  const match = (window.location.hash || "").match(/[#&]token=([^&]+)/);
  if (!match) return "";
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return match[1];
  }
}

function clearHashToken() {
  if (!window.location.hash) return;
  const cleaned = window.location.hash.replace(/[#&]token=[^&]*/, "");
  try {
    history.replaceState(null, "", window.location.pathname + window.location.search + (cleaned === "#" ? "" : cleaned));
  } catch {
    // ignore history failures in locked-down webviews
  }
}

function initialToken() {
  const fromHash = readTokenFromHash().trim();
  if (fromHash) {
    localStorage.setItem("dfmcWebToken", fromHash);
    clearHashToken();
    return fromHash;
  }
  return (localStorage.getItem("dfmcWebToken") || "").trim();
}

function field<T = any>(value: AnyRecord | undefined, ...names: string[]): T | "" {
  for (const name of names) {
    if (value && value[name] !== undefined && value[name] !== null) return value[name] as T;
  }
  return "";
}

function shortID(id: string) {
  return id.length <= 14 ? id : `${id.slice(0, 14)}...`;
}

function stateClass(status: unknown) {
  return String(status || "pending").toLowerCase();
}

function taskID(task: Task) {
  return String(field(task, "ID", "id") || "");
}

function taskParentID(task: Task) {
  return String(field(task, "ParentID", "parent_id") || "");
}

function taskRunID(task: Task) {
  return String(field(task, "RunID", "run_id") || "");
}

function taskTitle(task: Task) {
  return String(field(task, "Title", "title") || "(untitled)");
}

function percent(done: number, total: number) {
  return total > 0 ? Math.round((done / total) * 100) : 0;
}

function marker(status: unknown) {
  switch (stateClass(status)) {
    case "done":
      return "✓";
    case "running":
      return "…";
    case "blocked":
    case "failed":
      return "✗";
    case "skipped":
      return "⤳";
    case "waiting":
      return "⧖";
    case "external_review":
      return "⚠";
    default:
      return "○";
  }
}

function Pill({ status }: { status: unknown }) {
  const s = stateClass(status);
  return <Badge variant="outline" className={`pill ${s}`}>{s || "?"}</Badge>;
}

function KV({ label, value }: { label: string; value: unknown }) {
  return (
    <div className="kv">
      <strong>{label}</strong>
      <span>{String(value ?? "-")}</span>
    </div>
  );
}

export function App() {
  const [token, setTokenState] = useState(initialToken);
  const [status, setStatus] = useState<AnyRecord | null>(null);
  const [providers, setProviders] = useState<AnyRecord[]>([]);
  const [skills, setSkills] = useState<AnyRecord[]>([]);
  const [tools, setTools] = useState<AnyRecord[]>([]);
  const [files, setFiles] = useState<string[]>([]);
  const [activeFile, setActiveFile] = useState("");
  const [fileContent, setFileContent] = useState("Select a file from the list.");
  const [codemap, setCodemap] = useState<AnyRecord>({});
  const [diff, setDiff] = useState("Working tree is clean.");
  const [patch, setPatch] = useState("");
  const [patchStatus, setPatchStatus] = useState("Patch tools ready.");
  const [messages, setMessages] = useState<ChatMessage[]>([
    { role: "system", content: "Workbench ready. Ask something and the answer will stream here." },
  ]);
  const [chatInput, setChatInput] = useState("");
  const [chatStatus, setChatStatus] = useState("ready");
  const [activities, setActivities] = useState<ActivityEntry[]>([]);
  const [activityStatus, setActivityStatus] = useState("connecting...");
  const [activityFollow, setActivityFollow] = useState(true);
  const [driveActive, setDriveActive] = useState<DriveRun[]>([]);
  const [driveRuns, setDriveRuns] = useState<DriveRun[]>([]);
  const [driveSelected, setDriveSelected] = useState("");
  const [driveDetail, setDriveDetail] = useState<DriveRun | null>(null);
  const [driveTask, setDriveTask] = useState("");
  const [driveStatus, setDriveStatus] = useState("idle");
  const [driveSummary, setDriveSummary] = useState("No drive run loaded.");
  const [tasks, setTasks] = useState<Task[]>([]);
  const [taskSelected, setTaskSelected] = useState("");
  const [taskMode, setTaskMode] = useState<"list" | "tree" | "roots">("list");
  const [tasksStatus, setTasksStatus] = useState("idle");
  const [tasksSummary, setTasksSummary] = useState("No tasks loaded.");
  const [theme, setTheme] = useState<ThemeMode>(() => (localStorage.getItem("dfmcTheme") as ThemeMode) || "dark");
  const transcriptRef = useRef<HTMLDivElement>(null);
  const activityRef = useRef<HTMLDivElement>(null);

  const setToken = useCallback((next: string) => {
    const clean = next.trim();
    setTokenState(clean);
    if (clean) localStorage.setItem("dfmcWebToken", clean);
    else localStorage.removeItem("dfmcWebToken");
  }, []);

  useEffect(() => {
    const next = theme === "light" ? "light" : "dark";
    document.documentElement.classList.toggle("dark", next === "dark");
    localStorage.setItem("dfmcTheme", next);
  }, [theme]);

  const request = useCallback(
    async (url: string, options: RequestInit = {}): Promise<Response> => {
      const headers = new Headers(options.headers || {});
      if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
      return fetch(url, { ...options, headers });
    },
    [token],
  );

  const json = useCallback(
    async <T,>(url: string, options: RequestInit = {}): Promise<T> => {
      const resp = await request(url, options);
      if (resp.status === 401) {
        const provided = window.prompt("DFMC server requires a token. Enter token:");
        if (provided?.trim()) {
          setToken(provided);
          const headers = new Headers(options.headers || {});
          headers.set("Authorization", `Bearer ${provided.trim()}`);
          const retry = await fetch(url, { ...options, headers });
          if (!retry.ok) throw new Error((await retry.text()) || `HTTP ${retry.status}`);
          return retry.json();
        }
        setToken("");
        throw new Error("auth required");
      }
      if (!resp.ok) throw new Error((await resp.text()) || `HTTP ${resp.status}`);
      return resp.json();
    },
    [request, setToken],
  );

  const loadStatus = useCallback(async () => {
    const data = await json<AnyRecord>("/api/v1/status");
    setStatus(data);
  }, [json]);

  const loadCatalogs = useCallback(async () => {
    const [providerData, skillData, toolData] = await Promise.all([
      json<any>("/api/v1/providers"),
      json<any>("/api/v1/skills"),
      json<any>("/api/v1/tools"),
    ]);
    setProviders(Array.isArray(providerData?.providers) ? providerData.providers : []);
    setSkills(Array.isArray(skillData?.skills) ? skillData.skills : []);
    setTools(Array.isArray(toolData?.tools) ? toolData.tools : []);
  }, [json]);

  const loadFiles = useCallback(async () => {
    const data = await json<any>("/api/v1/files?limit=80");
    setFiles(Array.isArray(data?.files) ? data.files : []);
  }, [json]);

  const loadFile = useCallback(
    async (path: string) => {
      setActiveFile(path);
      const data = await json<any>(`/api/v1/files/${encodeURIComponent(path)}`);
      setFileContent(data?.content || "(empty file)");
    },
    [json],
  );

  const loadCodemap = useCallback(async () => {
    setCodemap(await json<AnyRecord>("/api/v1/codemap"));
  }, [json]);

  const loadDiff = useCallback(async () => {
    const data = await json<any>("/api/v1/workspace/diff");
    setDiff(data?.diff || "Working tree is clean.");
    const changed = Array.isArray(data?.changed_files) ? data.changed_files : [];
    setPatchStatus(data?.clean ? "Working tree is clean." : `Changed files: ${changed.length ? changed.join(", ") : "detected"}`);
  }, [json]);

  const loadLatestPatch = useCallback(async () => {
    const data = await json<any>("/api/v1/workspace/patch");
    setPatch(data?.patch || "");
    setPatchStatus(data?.patch ? "Loaded latest assistant patch." : "No assistant patch found yet.");
  }, [json]);

  const applyPatch = useCallback(
    async (checkOnly: boolean) => {
      const body = patch.trim() ? { patch, check_only: checkOnly } : { source: "latest", check_only: checkOnly };
      const data = await json<any>("/api/v1/workspace/apply", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      setPatchStatus(data?.message || (checkOnly ? "Patch check complete." : "Patch applied."));
      await loadDiff();
    },
    [json, loadDiff, patch],
  );

  const loadTasks = useCallback(async () => {
    setTasksStatus("loading...");
    try {
      const data = await json<Task[]>("/api/v1/tasks?limit=200");
      const rows = Array.isArray(data) ? data : [];
      setTasks(rows);
      const driveOwned = rows.filter((task) => taskRunID(task)).length;
      setTasksStatus(rows.length ? `${rows.length} task(s)` : "empty");
      setTasksSummary(`${rows.length} task(s)${driveOwned ? ` · ${driveOwned} drive-owned` : ""}`);
      if (taskSelected && !rows.some((task) => taskID(task) === taskSelected)) setTaskSelected("");
    } catch (err) {
      setTasksStatus("load error");
      setTasksSummary(`Task load failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }, [json, taskSelected]);

  const refreshDrive = useCallback(async () => {
    const [active, runs] = await Promise.all([json<any[]>("/api/v1/drive/active"), json<any[]>("/api/v1/drive")]);
    setDriveActive(Array.isArray(active) ? active : []);
    setDriveRuns(Array.isArray(runs) ? runs : []);
    setDriveStatus(active?.length ? `${active.length} active` : "idle");
  }, [json]);

  const loadDriveDetail = useCallback(
    async (id: string) => {
      if (!id) {
        setDriveDetail(null);
        return;
      }
      const run = await json<DriveRun>(`/api/v1/drive/${encodeURIComponent(id)}`);
      setDriveDetail(run);
      const todos = Array.isArray(run?.todos) ? run.todos : [];
      const done = todos.filter((todo: AnyRecord) => stateClass(todo.status) === "done").length;
      setDriveSummary(`${run.id} · ${run.status} · ${done}/${todos.length} done`);
    },
    [json],
  );

  const selectDrive = useCallback(
    (id: string) => {
      setDriveSelected(id);
      void loadDriveDetail(id);
    },
    [loadDriveDetail],
  );

  const startDrive = useCallback(async () => {
    const task = driveTask.trim();
    if (!task) {
      setDriveSummary("Enter a task before starting.");
      return;
    }
    setDriveStatus("starting...");
    const resp = await request("/api/v1/drive", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ task, max_parallel: 1, max_todos: 20 }),
    });
    if (resp.status !== 202 && resp.status !== 200) throw new Error((await resp.text()) || `HTTP ${resp.status}`);
    setDriveTask("");
    setDriveSummary("Drive started. Watch for drive:* events.");
    window.setTimeout(() => void refreshDrive(), 400);
  }, [driveTask, refreshDrive, request]);

  const stopDrive = useCallback(
    async (id: string) => {
      const resp = await request(`/api/v1/drive/${encodeURIComponent(id)}/stop`, { method: "POST" });
      if (!resp.ok) throw new Error((await resp.text()) || `HTTP ${resp.status}`);
      setDriveSummary(`Stop signal sent for ${shortID(id)}.`);
      window.setTimeout(() => void refreshDrive(), 300);
    },
    [refreshDrive, request],
  );

  const deleteTask = useCallback(
    async (id: string) => {
      if (!id || !window.confirm(`Delete task ${id}?`)) return;
      const resp = await request(`/api/v1/tasks/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (!resp.ok) throw new Error((await resp.text()) || `HTTP ${resp.status}`);
      if (taskSelected === id) setTaskSelected("");
      await loadTasks();
    },
    [loadTasks, request, taskSelected],
  );

  const clearTasks = useCallback(async () => {
    const localTasks = tasks.filter((task) => !taskRunID(task));
    if (!localTasks.length) {
      setTasksSummary("/tasks clear: store is already empty or only drive-owned tasks remain.");
      return;
    }
    if (!window.confirm(`Clear ${localTasks.length} non-drive task(s)?`)) return;
    let deleted = 0;
    let firstError = "";
    for (const task of localTasks) {
      try {
        const resp = await request(`/api/v1/tasks/${encodeURIComponent(taskID(task))}`, { method: "DELETE" });
        if (!resp.ok) throw new Error((await resp.text()) || `HTTP ${resp.status}`);
        deleted++;
      } catch (err) {
        if (!firstError) firstError = err instanceof Error ? err.message : String(err);
      }
    }
    await loadTasks();
    const kept = tasks.filter((task) => taskRunID(task)).length;
    setTasksSummary(`Cleared ${deleted} task(s).${kept ? ` ${kept} drive-owned task(s) kept.` : ""}${firstError ? ` First error: ${firstError}` : ""}`);
  }, [loadTasks, request, tasks]);

  const sendChat = useCallback(async () => {
    const message = chatInput.trim();
    if (!message) return;
    setChatInput("");
    setChatStatus("streaming...");
    setMessages((prev) => [...prev, { role: "user", content: message }, { role: "assistant", content: "" }]);
    try {
      const resp = await request("/api/v1/chat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message }),
      });
      if (!resp.ok || !resp.body) throw new Error((await resp.text()) || `HTTP ${resp.status}`);
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        const chunk = decoder.decode(value, { stream: true });
        setMessages((prev) => {
          const next = prev.slice();
          const last = next[next.length - 1];
          next[next.length - 1] = { ...last, content: last.content + chunk };
          return next;
        });
      }
      setChatStatus("ready");
    } catch (err) {
      setChatStatus("error");
      setMessages((prev) => [...prev, { role: "system", content: err instanceof Error ? err.message : String(err) }]);
    }
  }, [chatInput, request]);

  const classifyActivity = useCallback((data: AnyRecord): ActivityEntry => {
    const type = String(data.event || data.type || "event").toLowerCase();
    const payload = data.payload || {};
    const rid = payload.run_id || payload.id || "";
    let entry = { kind: "stream", icon: "•", text: type };
    if (type.startsWith("drive:")) {
      entry = { kind: "agent", icon: "▸", text: `${type.replace("drive:", "drive ")} ${shortID(String(rid))} ${payload.task || payload.title || payload.error || ""}` };
    } else if (type.startsWith("tool:")) {
      entry = { kind: "tool", icon: "⌁", text: `${type} ${payload.name || payload.tool || ""}` };
    } else if (type.includes("error") || payload.error) {
      entry = { kind: "error", icon: "✗", text: `${type} ${payload.error || ""}` };
    } else if (type.includes("context")) {
      entry = { kind: "ctx", icon: "◌", text: type };
    }
    return { id: Date.now() + Math.random(), ts: new Date().toLocaleTimeString(), ...entry };
  }, []);

  useEffect(() => {
    void Promise.all([loadStatus(), loadCatalogs(), loadFiles(), loadCodemap(), loadDiff(), refreshDrive(), loadTasks()]).catch((err) => {
      setMessages((prev) => [...prev, { role: "system", content: `Workbench load error: ${err instanceof Error ? err.message : String(err)}` }]);
    });
  }, [loadCatalogs, loadCodemap, loadDiff, loadFiles, loadStatus, loadTasks, refreshDrive]);

  useEffect(() => {
    let closed = false;
    let controller: AbortController | null = null;
    const connect = async () => {
      while (!closed) {
        controller = new AbortController();
        try {
          const resp = await request("/ws", { signal: controller.signal });
          if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`);
          setActivityStatus("connected");
          const reader = resp.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          while (!closed) {
            const { value, done } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            const frames = buffer.split("\n\n");
            buffer = frames.pop() || "";
            for (const frame of frames) {
              const line = frame.split("\n").find((item) => item.startsWith("data: "));
              if (!line) continue;
              const data = JSON.parse(line.slice(6));
              if (!data || data.type !== "event") continue;
              setActivities((prev) => [...prev.slice(-activityLimit + 1), classifyActivity(data)]);
              const evType = String(data.event || "").toLowerCase();
              if (evType.startsWith("drive:")) {
                void refreshDrive();
                void loadTasks();
              }
            }
          }
        } catch {
          if (!closed) setActivityStatus("retrying...");
        }
        await new Promise((resolve) => window.setTimeout(resolve, 1500));
      }
    };
    void connect();
    return () => {
      closed = true;
      controller?.abort();
    };
  }, [classifyActivity, loadTasks, refreshDrive, request]);

  useEffect(() => {
    transcriptRef.current?.scrollTo({ top: transcriptRef.current.scrollHeight });
  }, [messages]);

  useEffect(() => {
    if (activityFollow) activityRef.current?.scrollTo({ top: activityRef.current.scrollHeight });
  }, [activities, activityFollow]);

  const selectedTask = useMemo(() => tasks.find((task) => taskID(task) === taskSelected), [taskSelected, tasks]);
  const taskRows = useMemo(() => {
    if (taskMode === "list") return tasks;
    return tasks.filter((task) => !taskParentID(task));
  }, [taskMode, tasks]);
  const codemapNodes = Array.isArray(codemap.nodes) ? codemap.nodes : [];
  const codemapEdges = Array.isArray(codemap.edges) ? codemap.edges : [];
  const recentRuns = useMemo(
    () =>
      driveRuns
        .slice()
        .sort((a, b) => (Date.parse(b.created_at || b.CreatedAt || "") || 0) - (Date.parse(a.created_at || a.CreatedAt || "") || 0))
        .slice(0, 12),
    [driveRuns],
  );
  const taskStats = useMemo(() => {
    const done = tasks.filter((task) => stateClass(field(task, "State", "state")) === "done").length;
    const running = tasks.filter((task) => stateClass(field(task, "State", "state")) === "running").length;
    const blocked = tasks.filter((task) => ["blocked", "failed"].includes(stateClass(field(task, "State", "state")))).length;
    const driveOwned = tasks.filter((task) => taskRunID(task)).length;
    return { done, running, blocked, driveOwned, completion: percent(done, tasks.length) };
  }, [tasks]);

  return (
    <div className="shell">
      <section className="hero">
        <div className="panel hero-card">
          <div className="hero-top">
            <div className="eyebrow"><Sparkles size={14} /> DFMC Workbench</div>
            <Button
              variant="secondary"
              size="icon"
              title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
              onClick={() => setTheme((value) => value === "dark" ? "light" : "dark")}
            >
              {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
            </Button>
          </div>
          <h1>TUI-first operator surface, React 19 WebUI.</h1>
          <p className="lede">Chat, files, patches, Drive, and task-store views share the same engine semantics as the terminal workbench.</p>
          <div className="chips">
            <div className="chip"><strong>Provider</strong><span>{status?.provider || "-"}</span></div>
            <div className="chip"><strong>Model</strong><span>{status?.model || "-"}</span></div>
            <div className="chip"><strong>Project</strong><span>{status?.project_root || status?.project || "-"}</span></div>
          </div>
        </div>
        <div className="panel stats-grid">
          <KV label="AST" value={status?.ast_backend || status?.ast || "-"} />
          <KV label="Providers" value={providers.length} />
          <KV label="Tools" value={tools.length} />
          <KV label="Skills" value={skills.length} />
        </div>
      </section>

      <section className="workspace">
        <div className="stack">
          <Panel title="Files" subtitle="Project file browser." icon={<FileText size={16} />} action={<Button variant="secondary" size="sm" onClick={loadFiles}><RefreshCw size={14} />Refresh</Button>}>
            <div className="list">
              {files.length ? files.map((file) => (
                <button key={file} className={`list-item ${file === activeFile ? "active" : ""}`} onClick={() => void loadFile(file)}>{file}</button>
              )) : <div className="list-item idle">No files loaded.</div>}
            </div>
            <pre className="codebox">{fileContent}</pre>
          </Panel>
          <Panel title="Workspace Patch" subtitle="Load, check, and apply the latest assistant diff." icon={<Code2 size={16} />} action={<Button variant="secondary" size="sm" onClick={loadDiff}><RefreshCw size={14} />Refresh</Button>}>
            <Textarea value={patch} onChange={(event) => setPatch(event.target.value)} placeholder="Paste unified diff or load latest assistant patch." />
            <div className="action-row">
              <Button variant="secondary" size="sm" onClick={loadLatestPatch}>Load latest</Button>
              <Button variant="secondary" size="sm" onClick={() => void applyPatch(true)}>Check</Button>
              <Button size="sm" onClick={() => void applyPatch(false)}><Zap size={14} />Apply</Button>
            </div>
            <div className="inline-note">{patchStatus}</div>
            <pre className="codebox">{diff}</pre>
          </Panel>
        </div>

        <Panel title="Chat" subtitle="Streams through /api/v1/chat." icon={<Bot size={16} />} action={<Button variant="secondary" size="sm" onClick={() => void Promise.all([loadStatus(), loadCatalogs()])}><BrainCircuit size={14} />Status</Button>}>
          <div className="transcript" ref={transcriptRef}>
            {messages.map((msg, idx) => (
              <div className={`message ${msg.role}`} key={idx}>
                <div className="role">{msg.role}</div>
                <pre>{msg.content}</pre>
              </div>
            ))}
          </div>
          <Textarea value={chatInput} onChange={(event) => setChatInput(event.target.value)} onKeyDown={(event) => submitOnModEnter(event, sendChat)} placeholder="Ask DFMC..." />
          <div className="chat-controls">
            <Button onClick={() => void sendChat()}><Send size={14} />Send</Button>
            <span className="inline-note">{chatStatus}</span>
          </div>
        </Panel>

        <div className="stack">
          <Panel title="Activity" subtitle="Live engine event stream." icon={<Activity size={16} />} action={<Button variant="secondary" size="sm" onClick={() => setActivities([])}><Trash2 size={14} />Clear</Button>}>
            <div className="pulse">{activityStatus}</div>
            <div className="activity-log" ref={activityRef}>
              {activities.length ? activities.map((entry) => (
                <div key={entry.id} className={`activity-row kind-${entry.kind}`}>
                  <span className="ts">{entry.ts}</span><span className="kind">{entry.icon}</span><span>{entry.text}</span>
                </div>
              )) : <div className="activity-empty">No events yet.</div>}
            </div>
            <Button variant="secondary" size="sm" onClick={() => setActivityFollow((value) => !value)}>{activityFollow ? "Pause follow" : "Resume follow"}</Button>
          </Panel>
          <Panel title="CodeMap Pulse" subtitle="Structural readout." icon={<GitBranch size={16} />} action={<Button variant="secondary" size="sm" onClick={loadCodemap}><RefreshCw size={14} />Refresh</Button>}>
            <div className="mini-grid"><KV label="Nodes" value={codemapNodes.length} /><KV label="Edges" value={codemapEdges.length} /></div>
            <div className="graph-list">
              {codemapNodes.slice(0, 8).map((node: AnyRecord, idx: number) => (
                <div className="graph-item" key={idx}><strong>{node.name || node.id || "node"}</strong><span>{node.kind || "node"}{node.path ? ` · ${node.path}` : ""}</span></div>
              ))}
            </div>
          </Panel>
          <Panel title="Capabilities" subtitle="Providers, skills, and tools." icon={<Boxes size={16} />}>
            <div className="mini-grid"><KV label="Providers" value={providers.length} /><KV label="Skills" value={skills.length} /></div>
            <pre className="codebox">{tools.slice(0, 30).map((tool) => tool.name || tool.Name).filter(Boolean).join("\n") || "Loading tool catalog..."}</pre>
          </Panel>
        </div>

        <Panel title="Drive Cockpit" subtitle="Autonomous plan/execute loop." icon={<Workflow size={16} />} className="wide" action={<span className="pulse">{driveStatus}</span>}>
          <Textarea value={driveTask} onChange={(event) => setDriveTask(event.target.value)} onKeyDown={(event) => submitOnModEnter(event, startDrive)} placeholder="Describe the autonomous task." />
          <div className="drive-controls">
            <Button onClick={() => void startDrive()}><Play size={14} />Start drive</Button>
            <Button variant="secondary" size="sm" onClick={() => void refreshDrive()}><RefreshCw size={14} />Refresh</Button>
            <span className="inline-note">{driveSummary}</span>
          </div>
          <div className="drive-grid">
            <div className="drive-side">
              <DriveList title="Active in this process" runs={driveActive} selected={driveSelected} onSelect={(run) => selectDrive(String(run.run_id || run.id || ""))} onStop={stopDrive} />
              <DriveList title="Recent runs" runs={recentRuns} selected={driveSelected} onSelect={(run) => selectDrive(String(run.id || ""))} />
            </div>
            <DriveDetail run={driveDetail} />
          </div>
        </Panel>

        <Panel title="Tasks" subtitle="Matches TUI /tasks list/tree/roots/show/clear." icon={<CheckCircle2 size={16} />} className="wide" action={<span className="pulse">{tasksStatus}</span>}>
          <div className="drive-controls">
            <Button variant="secondary" size="sm" onClick={loadTasks}><RefreshCw size={14} />Refresh</Button>
            <TabsList aria-label="Task view mode">
              <TabsTrigger active={taskMode === "list"} onClick={() => setTaskMode("list")}>List</TabsTrigger>
              <TabsTrigger active={taskMode === "tree"} onClick={() => setTaskMode("tree")}>Tree</TabsTrigger>
              <TabsTrigger active={taskMode === "roots"} onClick={() => setTaskMode("roots")}>Roots</TabsTrigger>
            </TabsList>
            <Button variant="secondary" size="sm" onClick={() => void clearTasks()}><Trash2 size={14} />Clear</Button>
            <span className="inline-note">{tasksSummary}</span>
          </div>
          <div className="summary-grid">
            <div className="summary-item">
              <span>Completion</span>
              <strong>{taskStats.completion}%</strong>
              <Progress value={taskStats.completion} />
            </div>
            <div className="summary-item"><span>Done</span><strong>{taskStats.done}</strong></div>
            <div className="summary-item"><span>Running</span><strong>{taskStats.running}</strong></div>
            <div className="summary-item"><span>Blocked</span><strong>{taskStats.blocked}</strong></div>
            <div className="summary-item"><span>Drive-owned</span><strong>{taskStats.driveOwned}</strong></div>
          </div>
          <div className="task-grid">
            <div className="task-list">
              {taskRows.length ? taskRows.map((task) => (
                <TaskRow key={taskID(task)} task={task} tasks={tasks} mode={taskMode} depth={0} selected={taskSelected} onSelect={setTaskSelected} />
              )) : <div className="list-item idle">(no tasks)</div>}
            </div>
            <TaskDetail task={selectedTask} onDelete={deleteTask} />
          </div>
        </Panel>
      </section>
    </div>
  );
}

function Panel({ title, subtitle, icon, action, className = "", children }: { title: string; subtitle: string; icon?: ReactNode; action?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <div className={`panel ${className}`}>
      <div className="pane-header">
        <div className="pane-heading">
          {icon && <span className="pane-icon">{icon}</span>}
          <div><div className="pane-title">{title}</div><div className="pane-subtitle">{subtitle}</div></div>
        </div>
        {action}
      </div>
      <div className="pane-body">{children}</div>
    </div>
  );
}

function submitOnModEnter(event: KeyboardEvent<HTMLTextAreaElement>, fn: () => void | Promise<void>) {
  if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
    event.preventDefault();
    void fn();
  }
}

function DriveList({ title, runs, selected, onSelect, onStop }: { title: string; runs: DriveRun[]; selected: string; onSelect: (run: DriveRun) => void; onStop?: (id: string) => void | Promise<void> }) {
  return (
    <div>
      <div className="drive-section-title">{title}</div>
      <div className="list">
        {runs.length ? runs.map((run) => {
          const id = String(run.run_id || run.id || "");
          return (
            <div className={`drive-item ${id === selected ? "active" : ""}`} key={id} onClick={() => onSelect(run)}>
              <div className="drive-item-id">{shortID(id)}</div>
              <div className="drive-item-task">{run.task || "(no task)"}</div>
              <div className="drive-item-meta"><Pill status={run.status || "running"} />{onStop && <Button variant="secondary" size="sm" className="micro" onClick={(event) => { event.stopPropagation(); void onStop(id); }}>Stop</Button>}</div>
            </div>
          );
        }) : <div className="list-item idle">None.</div>}
      </div>
    </div>
  );
}

function DriveDetail({ run }: { run: DriveRun | null }) {
  if (!run) return <div className="drive-detail"><div className="drive-detail-empty">Select a run to inspect its TODO ladder, or start a new one.</div></div>;
  const todos = Array.isArray(run.todos) ? run.todos : [];
  const done = todos.filter((todo: AnyRecord) => stateClass(todo.status) === "done").length;
  const completion = percent(done, todos.length);
  return (
    <div className="drive-detail">
      <div className="drive-item-meta"><span className="drive-item-id">{run.id}</span><Pill status={run.status} /><span>{done}/{todos.length} done</span></div>
      <div className="detail-progress">
        <span>TODO completion</span>
        <strong>{completion}%</strong>
        <Progress value={completion} />
      </div>
      <div>{run.task || "(no task)"}</div>
      {todos.length ? todos.map((todo: AnyRecord) => (
        <div className={`drive-todo ${stateClass(todo.status)}`} key={todo.id}>
          <div className="marker">{marker(todo.status)}</div>
          <div className="body">
            <div className="title">{todo.id} · {todo.title || "(untitled)"}</div>
            {todo.detail && <div className="detail">{todo.detail}</div>}
            {todo.brief && <div className="brief">{todo.brief}</div>}
            {todo.error && <div className="err">{todo.error}</div>}
          </div>
          <Pill status={todo.status} />
        </div>
      )) : <div className="drive-detail-empty">No TODOs were emitted.</div>}
    </div>
  );
}

function TaskRow({ task, tasks, mode, depth, selected, onSelect }: { task: Task; tasks: Task[]; mode: string; depth: number; selected: string; onSelect: (id: string) => void }) {
  const id = taskID(task);
  const children = mode === "tree" ? tasks.filter((row) => taskParentID(row) === id) : [];
  return (
    <>
      <div className={`task-row ${id === selected ? "active" : ""}`} style={{ marginLeft: depth ? Math.min(depth * 18, 72) : 0 }} onClick={() => onSelect(id)}>
        <div className="task-title">{marker(field(task, "State", "state"))} {taskTitle(task)}</div>
        <div className="task-meta"><Pill status={field(task, "State", "state") || "pending"} />{field(task, "WorkerClass", "worker_class") && <span>[{String(field(task, "WorkerClass", "worker_class"))}]</span>}{taskRunID(task) && <span>drive {shortID(taskRunID(task))}</span>}</div>
      </div>
      {children.map((child) => <TaskRow key={taskID(child)} task={child} tasks={tasks} mode={mode} depth={depth + 1} selected={selected} onSelect={onSelect} />)}
    </>
  );
}

function TaskDetail({ task, onDelete }: { task?: Task; onDelete: (id: string) => void | Promise<void> }) {
  if (!task) return <div className="task-detail"><div className="task-detail-empty">Select a task to inspect worker, verification, labels, and context.</div></div>;
  const lines = [
    `▸ ${taskTitle(task)}  [${stateClass(field(task, "State", "state"))}]`,
    ["id:", taskID(task)],
    ["detail:", field(task, "Detail", "detail")],
    ["parent:", taskParentID(task)],
    ["run:", taskRunID(task)],
    ["depends:", field(task, "DependsOn", "depends_on")],
    ["blocked:", field(task, "BlockedReason", "blocked_reason")],
    ["worker:", field(task, "WorkerClass", "worker_class")],
    ["labels:", field(task, "Labels", "labels")],
    ["verify:", field(task, "Verification", "verification")],
    ["summary:", field(task, "Summary", "summary")],
    ["error:", field(task, "Error", "error")],
  ]
    .map((line) => Array.isArray(line) ? (Array.isArray(line[1]) ? [line[0], line[1].join(", ")] : line) : line)
    .filter((line) => typeof line === "string" || String(line[1] || "").trim())
    .map((line) => typeof line === "string" ? line : `${String(line[0]).padEnd(10, " ")}${line[1]}`)
    .join("\n");
  return (
    <div className="task-detail">
      <div className="drive-item-meta"><Pill status={field(task, "State", "state") || "pending"} /><Button variant="secondary" size="sm" className="micro push" onClick={() => void onDelete(taskID(task))}><Trash2 size={12} />Delete</Button></div>
      <pre>{lines}</pre>
    </div>
  );
}
