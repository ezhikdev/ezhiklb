import { Activity, ArrowDown, ArrowUp, Boxes, CheckCircle2, ChevronDown, ChevronRight, CircleAlert, CircleGauge, Clock3, Copy, Cpu, Github, HeartPulse, Hexagon, History, LoaderCircle, LogOut, MemoryStick, Network, Pencil, Plus, Power, RefreshCw, Save, ScrollText, Server, Settings, ShieldCheck, Trash2, Users, X } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { ApiError, api } from "./lib/api"
import type { AuditEvent, BackendHealth, NodeInfo, NodeMetricPoint, Profile, ProfileConfig, Revision, ServiceStat, Status, SystemSettings } from "./types"
import { ProfileEditor } from "./components/ProfileEditor"
import { Badge, Button, Card, ConfirmDialog, Dialog, EmptyState, Field, Input, SelectMenu, Switch } from "./components/ui"

type Page = "overview" | "profiles" | "nodes" | "health" | "events" | "settings"

const nav = [
  ["overview", "Обзор", CircleGauge], ["profiles", "Профили", Boxes], ["nodes", "Ноды", Server],
  ["health", "Health", HeartPulse], ["events", "Журнал", ScrollText], ["settings", "Настройки", Settings],
] as const

const emptyConfig = (): ProfileConfig => ({ schema_version: 1, health_check: { enabled: true, interval_seconds: 10, timeout_millis: 1000, failure_threshold: 3, recovery_threshold: 2 }, listeners: [] })
const releaseVersion = "1.0.3"
const shellArg = (value: string) => `'${value.replace(/'/g, `'"'"'`)}'`
const updateStageInfo: Record<string, { percent: number; label: string }> = {
  requested: { percent: 8, label: "Отправлен запрос…" },
  updating: { percent: 15, label: "Подготовка обновления…" },
  downloading: { percent: 32, label: "Скачивание релиза…" },
  verifying: { percent: 58, label: "Проверка SHA-256…" },
  installing: { percent: 78, label: "Установка…" },
  restarting: { percent: 93, label: "Перезапуск агента…" },
}

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [page, setPageState] = useState<Page>(() => {
    const candidate = location.hash.slice(1) as Page
    return nav.some(([id]) => id === candidate) ? candidate : "overview"
  })
  const [status, setStatus] = useState<Status | null>(null)
  const [profiles, setProfiles] = useState<Profile[]>([])
  const [nodes, setNodes] = useState<NodeInfo[]>([])
  const [health, setHealth] = useState<BackendHealth[]>([])
  const [stats, setStats] = useState<ServiceStat[]>([])
  const [settings, setSettings] = useState<SystemSettings>({ panel_port: Number(location.port) || 80, agent_port: 8081 })
  const [error, setError] = useState("")
  const [editing, setEditing] = useState<{ profile: Profile; revision: Revision } | null>(null)
  const [creating, setCreating] = useState(false)

  const load = useCallback(async () => {
    try {
      const [nextStatus, nextProfiles, nextNodes, nextHealth, nextStats, nextSettings] = await Promise.all([api.status(), api.profiles(), api.nodes(), api.health(), api.stats(), api.settings()])
      setStatus(nextStatus); setProfiles(nextProfiles); setNodes(nextNodes); setHealth(nextHealth); setStats(nextStats); setSettings(nextSettings); setAuthenticated(true); setError("")
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 401) setAuthenticated(false)
      else setError(reason instanceof Error ? reason.message : "Не удалось загрузить данные")
    }
  }, [])

  useEffect(() => { void load() }, [load])
  useEffect(() => {
    if (!authenticated) return
    const timer = window.setInterval(() => { void load() }, 15000)
    return () => window.clearInterval(timer)
  }, [authenticated, load])
  useEffect(() => {
    const onHash = () => { const candidate = location.hash.slice(1) as Page; if (nav.some(([id]) => id === candidate)) setPageState(candidate) }
    window.addEventListener("hashchange", onHash)
    return () => window.removeEventListener("hashchange", onHash)
  }, [])
  const setPage = (next: Page) => { setPageState(next); history.pushState(null, "", `#${next}`); document.getElementById("main-content")?.focus() }
  const openProfile = async (profile: Profile) => {
    try { setEditing(await api.profile(profile.id)) } catch (reason) { setError(reason instanceof Error ? reason.message : "Не удалось открыть профиль") }
  }
  if (authenticated === null) return <div className="splash"><Hexagon /><span>EzhikLB</span></div>
  if (!authenticated) return <Login onSuccess={load} />

  return <><a className="skip-link" href="#main-content">Перейти к содержимому</a><div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><div className="brand__mark"><Hexagon /></div><div><strong>EzhikLB</strong><span>load balancer</span></div></div>
      <nav aria-label="Основная навигация">{nav.map(([id, label, Icon]) => <button key={id} className={page === id ? "active" : ""} onClick={() => setPage(id)}><Icon /><span>{label}</span>{page === id && <span className="nav-indicator" />}</button>)}</nav>
      <div className="sidebar__footer"><a className="github-link" href="https://github.com/ezhikdev/ezhiklb" target="_blank" rel="noreferrer"><Github /><span>GitHub проекта</span><ChevronRight /></a><div className="version"><span className="live-dot" />{status?.version?.includes("-") ? "pre-release" : "stable"} · {status?.version}</div><Button variant="ghost" className="logout" onClick={async () => { await api.logout(); setAuthenticated(false) }}><LogOut data-icon="inline-start" />Выйти</Button></div>
    </aside>
    <main id="main-content" tabIndex={-1} className="main-content">
      {error && <div className="error-banner" role="alert"><span>{error}</span><button onClick={() => setError("")} aria-label="Закрыть">×</button></div>}
      {page === "overview" && <Overview status={status} nodes={nodes} profiles={profiles} stats={stats} navigate={setPage} />}
      {page === "profiles" && <Profiles profiles={profiles} nodes={nodes} onCreate={() => setCreating(true)} onOpen={openProfile} onChanged={load} />}
      {page === "nodes" && <Nodes nodes={nodes} profiles={profiles} settings={settings} stats={stats} health={health} onChanged={load} />}
      {page === "health" && <Health items={health} nodes={nodes} />}
      {page === "events" && <Events nodes={nodes} profiles={profiles} />}
      {page === "settings" && <SettingsPage current={settings} />}
    </main>
    {(editing || creating) && <ProfileDialog existing={editing} health={health} nodes={nodes} onClose={() => { setEditing(null); setCreating(false) }} onSaved={async () => { setEditing(null); setCreating(false); await load() }} />}
  </div></>
}

function Login({ onSuccess }: { onSuccess: () => Promise<void> }) {
  const [token, setToken] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false)
  const submit = async (event: React.FormEvent) => { event.preventDefault(); setBusy(true); try { await api.login(token); await onSuccess() } catch (reason) { setError(reason instanceof Error ? reason.message : "Ошибка входа") } finally { setBusy(false) } }
  return <div className="login-page"><div className="login-glow" /><form className="login-card" onSubmit={submit}><div className="brand brand--login"><div className="brand__mark"><Hexagon /></div><div><strong>EzhikLB</strong><span>control plane</span></div></div><div><p className="eyebrow">Авторизация</p><h1>Добро пожаловать</h1><p>Введите токен администратора, созданный установщиком.</p></div><Field label="Admin token" error={error}><Input type="password" autoComplete="current-password" value={token} onChange={(e) => { setToken(e.target.value); setError("") }} autoFocus /></Field><Button type="submit" disabled={busy || token.length < 24}>{busy ? "Проверяю…" : "Войти в панель"}<ChevronRight data-icon="inline-end" /></Button><div className="secure-note"><ShieldCheck /><span>Токен сохраняется только в защищённой HTTP-only cookie.</span></div></form></div>
}

function Overview({ status, nodes, profiles, stats, navigate }: { status: Status | null; nodes: NodeInfo[]; profiles: Profile[]; stats: ServiceStat[]; navigate: (page: Page) => void }) {
  const services = stats.filter((item) => !item.backend_address)
  const packets = services.reduce((sum, item) => sum + item.incoming_packets, 0)
  const bytes = services.reduce((sum, item) => sum + item.incoming_bytes, 0)
  const [chartNode, setChartNode] = useState("all")
  const [history, setHistory] = useState<NodeMetricPoint[]>([])
  useEffect(() => { let active = true; const loadHistory = () => { void api.metricHistory(chartNode).then((items) => { if (active) setHistory(items) }).catch(() => { if (active) setHistory([]) }) }; loadHistory(); const timer = window.setInterval(loadHistory, 60000); return () => { active = false; window.clearInterval(timer) } }, [chartNode])
  const chartPoints = aggregateMetricHistory(history, chartNode === "all")
  const errorNodes = nodes.filter((node) => Boolean(node.last_error) || node.apply_state === "error").length
  return <div className="page"><PageHeader eyebrow="Control plane" title="Обзор" description="Состояние инфраструктуры EzhikLB в одном месте." />
    <div className="metrics-grid"><Metric label="Ноды онлайн" value={`${status?.online_nodes ?? 0}/${status?.nodes ?? 0}`} icon={<Server />} tone="success" /><Metric label="Активные записи" value={String(status?.listeners ?? 0)} icon={<Network />} /><Metric label="Входящие пакеты" value={formatNumber(packets)} icon={<Activity />} /><Metric label="Принято трафика" value={formatBytes(bytes)} icon={<CircleGauge />} /></div>
    <div className="overview-grid"><Card className="panel-card"><div className="panel-card__header"><div><p className="eyebrow">Ноды</p><h2>Состояние применения</h2></div><Button variant="ghost" onClick={() => navigate("nodes")}>Все ноды<ChevronRight data-icon="inline-end" /></Button></div><div className="node-list">{nodes.map((node) => <NodeRow key={node.id} node={node} profile={profiles.find((profile) => profile.id === node.profile_id)} />)}</div></Card><Card className="panel-card system-summary"><div className="panel-card__header"><div><p className="eyebrow">Система</p><h2>Состояние EzhikLB</h2></div><Badge tone={errorNodes ? "danger" : "success"}>{errorNodes ? `${errorNodes} ошибок` : "всё работает"}</Badge></div><div className="system-summary__grid"><div><span>Панель</span><strong>{status?.version || "—"}</strong></div><div><span>Связь с нодами</span><strong>{status?.online_nodes ?? 0} из {status?.nodes ?? 0}</strong></div><div><span>Профили</span><strong>{status?.profiles ?? 0}</strong></div><div><span>Ошибки применения</span><strong>{errorNodes}</strong></div></div></Card></div>
    <TrafficPanel stats={stats} nodes={nodes} />
    <section className="chart-section"><div className="chart-section__header"><div><p className="eyebrow">Последние 24 часа</p><h2>Нагрузка и активность</h2></div><div className="chart-node-select"><span>Показывать</span><SelectMenu compact label="Нода для графиков" value={chartNode} onChange={setChartNode} options={[{ value: "all", label: "Все ноды", description: "Общие значения" }, ...nodes.map((node) => ({ value: node.id, label: node.name, description: node.observed_address || node.ingress_address }))]} /></div></div><div className="chart-grid"><MetricChart title="Сеть" icon={<Network />} points={chartPoints} series={[{ key: "network_rx_bps", label: "Приём", color: "success" }, { key: "network_tx_bps", label: "Отдача", color: "accent" }]} format={formatNetworkRate} /><MetricChart title="CPU" icon={<Cpu />} points={chartPoints} series={[{ key: "cpu_used_percent", label: "Загрузка", color: "warning" }]} format={formatPercent} /><MetricChart title="RAM" icon={<MemoryStick />} points={chartPoints} series={[{ key: "ram_used_percent", label: "Использовано", color: "accent" }]} format={formatPercent} /><MetricChart title="Активные IP" icon={<Users />} points={chartPoints} series={[{ key: "active_ips", label: "Уникальные IP", color: "success" }]} format={(value) => formatNumber(value)} /></div></section>
  </div>
}

function TrafficPanel({ stats, nodes }: { stats: ServiceStat[]; nodes: NodeInfo[] }) {
  const nodeIDs = Array.from(new Set(stats.map((item) => item.node_id)))
  const [open, setOpen] = useState<Set<string>>(() => new Set())
  const toggle = (nodeID: string) => setOpen((current) => { const next = new Set(current); if (next.has(nodeID)) next.delete(nodeID); else next.add(nodeID); return next })
  return <Card className="traffic-card"><div className="panel-card__header"><div><p className="eyebrow">Live IPVS</p><h2>Фактическое распределение по нодам</h2></div><span className="traffic-updated">обновление каждые 15 секунд</span></div>
    {stats.length === 0 ? <EmptyState icon={<Activity />} title="Трафика пока нет" description="Счётчики появятся после применения хотя бы одной записи." /> : <div className="traffic-nodes">{nodeIDs.map((nodeID) => {
      const node = nodes.find((item) => item.id === nodeID)
      const items = stats.filter((item) => item.node_id === nodeID)
      const services = items.filter((item) => !item.backend_address)
      const expanded = open.has(nodeID)
      return <section className="traffic-node" key={nodeID}><button type="button" className="traffic-node__trigger" aria-expanded={expanded} onClick={() => toggle(nodeID)}><div className="node-name"><div className="node-avatar"><Server /></div><div><strong>{node?.name ?? nodeID}</strong><span>{services.length} маршрутов · {formatBytes(services.reduce((sum, item) => sum + item.incoming_bytes, 0))}</span></div></div><div className="traffic-node__summary"><Badge tone={node?.status === "online" ? "success" : node?.status === "error" ? "danger" : "neutral"}>{nodeStatusLabel(node?.status)}</Badge><ChevronDown className={expanded ? "chevron-open" : ""} /></div></button><div className={`traffic-node__content ${expanded ? "traffic-node__content--open" : ""}`}><div><div className="traffic-table"><div className="traffic-table__head"><span>Маршрут</span><span>Соединения</span><span>Пакеты</span><span>Входящий трафик</span></div>{items.map((item) => <div className={`traffic-table__row ${item.backend_address ? "traffic-table__row--backend" : ""}`} key={`${item.protocol}-${item.listen_address}-${item.listen_port}-${item.backend_address}-${item.backend_port}`}><div><Badge>{item.protocol.toUpperCase()}</Badge><span className="mono">{item.backend_address ? `↳ ${item.backend_address}:${item.backend_port}` : `${item.listen_address}:${item.listen_port}`}</span></div><strong className="mono">{formatNumber(item.connections)}</strong><strong className="mono">{formatNumber(item.incoming_packets)}</strong><strong className="mono">{formatBytes(item.incoming_bytes)}</strong></div>)}</div></div></div></section>
    })}</div>}
  </Card>
}

function Profiles({ profiles, nodes, onCreate, onOpen, onChanged }: { profiles: Profile[]; nodes: NodeInfo[]; onCreate: () => void; onOpen: (profile: Profile) => void; onChanged: () => Promise<void> }) {
  const [cloning, setCloning] = useState<Profile | null>(null)
  const [cloneName, setCloneName] = useState("")
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState<Profile | null>(null)
  return <div className="page"><PageHeader eyebrow="Desired state" title="Профили" description="Один профиль можно назначить нескольким нодам." action={<Button onClick={onCreate}><Plus data-icon="inline-start" />Новый профиль</Button>} />
    <div className="profile-grid">{profiles.map((profile) => {
      const assigned = nodes.filter((node) => node.profile_id === profile.id).length
      return <Card key={profile.id} className="profile-card" onClick={() => onOpen(profile)} role="button" tabIndex={0} onKeyDown={(event) => (event.key === "Enter" || event.key === " ") && onOpen(profile)}>
        <div className="profile-card__top"><div className="profile-icon"><Boxes /></div><div className="profile-card__actions"><Badge>{profile.version}</Badge><Button variant="ghost" className="icon-button" title="Клонировать профиль" aria-label={`Клонировать ${profile.name}`} onClick={(event) => { event.stopPropagation(); setCloning(profile); setCloneName(`${profile.name} — копия`) }}><Copy /></Button><Button variant="ghost" className="icon-button danger-hover" disabled={assigned > 0} title={assigned ? "Сначала назначьте нодам другой профиль" : "Удалить профиль"} aria-label={`Удалить ${profile.name}`} onClick={(event) => { event.stopPropagation(); setDeleting(profile) }}><Trash2 /></Button></div></div>
        <div><h2>{profile.name}</h2><p>{profile.description || "Без описания"}</p></div><div className="profile-card__meta"><span>{assigned} нод</span><span>Изменён {formatRelative(profile.updated_at)}</span></div>
      </Card>
    })}</div>
    {cloning && <Dialog title={`Клонирование · ${cloning.name}`} description="Будет создан независимый профиль с текущей конфигурацией." onClose={() => setCloning(null)}><div className="dialog__body"><Field label="Название копии"><Input value={cloneName} onChange={(event) => setCloneName(event.target.value)} autoFocus /></Field></div><div className="dialog__footer"><Button variant="ghost" onClick={() => setCloning(null)}>Отмена</Button><Button disabled={busy || !cloneName.trim()} onClick={async () => { setBusy(true); try { await api.cloneProfile(cloning.id, cloneName.trim()); setCloning(null); await onChanged() } finally { setBusy(false) } }}><Copy data-icon="inline-start" />{busy ? "Создаю…" : "Создать копию"}</Button></div></Dialog>}
    {deleting && <ConfirmDialog title="Удалить профиль?" description={`Профиль «${deleting.name}» и его история версий будут удалены без возможности восстановления.`} confirmLabel="Удалить профиль" danger busy={busy} onCancel={() => setDeleting(null)} onConfirm={async () => { setBusy(true); try { await api.deleteProfile(deleting.id); setDeleting(null); await onChanged() } finally { setBusy(false) } }} />}
  </div>
}

function Nodes({ nodes, profiles, settings, stats, health, onChanged }: { nodes: NodeInfo[]; profiles: Profile[]; settings: SystemSettings; stats: ServiceStat[]; health: BackendHealth[]; onChanged: () => Promise<void> }) {
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState("")
  const initialAgentURL = `${location.protocol}//${location.hostname}:${settings.agent_port}`
  const [agentURL, setAgentURL] = useState(initialAgentURL)
  const [credential, setCredential] = useState<{ node: NodeInfo; token: string } | null>(null)
  const [editingNode, setEditingNode] = useState<NodeInfo | null>(null)
  const [selectedNode, setSelectedNode] = useState<NodeInfo | null>(null)
  const [editName, setEditName] = useState("")
  const [busy, setBusy] = useState(false)
  const [enrollError, setEnrollError] = useState("")
  const [copied, setCopied] = useState(false)
  const [confirmNode, setConfirmNode] = useState<{ node: NodeInfo; action: "disable" | "delete" | "update" } | null>(null)
  const [toast, setToast] = useState<{ tone: "success" | "danger"; text: string } | null>(null)
  const toastTimer = useRef<number | null>(null)
  const notify = useCallback((next: { tone: "success" | "danger"; text: string }, duration: number) => {
    if (toastTimer.current !== null) window.clearTimeout(toastTimer.current)
    setToast(next)
    toastTimer.current = window.setTimeout(() => { setToast(null); toastTimer.current = null }, duration)
  }, [])
  useEffect(() => () => { if (toastTimer.current !== null) window.clearTimeout(toastTimer.current) }, [])
  const normalizedAgentURL = agentURL.trim().replace(/\/$/, "")
  const agentURLValid = /^https?:\/\/[^\s]+$/i.test(normalizedAgentURL)
  const insecureFlag = normalizedAgentURL.startsWith("http://") ? " EZHIKLB_ALLOW_INSECURE=1" : ""
  const installCommand = credential && agentURLValid ? `ezhik_tmp=$(mktemp -d) && cd "$ezhik_tmp" && sudo apt-get update && sudo apt-get install -y ca-certificates curl && curl -fLO https://github.com/ezhikdev/ezhiklb/releases/download/v${releaseVersion}/ezhiklb_${releaseVersion}_linux_amd64.tar.gz && curl -fLO https://github.com/ezhikdev/ezhiklb/releases/download/v${releaseVersion}/ezhiklb_${releaseVersion}_linux_amd64.tar.gz.sha256 && sha256sum -c ezhiklb_${releaseVersion}_linux_amd64.tar.gz.sha256 && tar -xzf ezhiklb_${releaseVersion}_linux_amd64.tar.gz && sudo env EZHIKLB_ROLE=node EZHIKLB_PANEL_URL=${shellArg(normalizedAgentURL)} EZHIKLB_NODE_ID=${shellArg(credential.node.id)} EZHIKLB_AGENT_TOKEN=${shellArg(credential.token)}${insecureFlag} ./install.sh && cd / && rm -rf -- "$ezhik_tmp"` : ""
  const liveCredentialNode = credential ? nodes.find((node) => node.id === credential.node.id) ?? credential.node : null
  useEffect(() => {
    if (!credential || liveCredentialNode?.status === "online") return
    const timer = window.setInterval(() => { void onChanged() }, 3000)
    return () => window.clearInterval(timer)
  }, [credential, liveCredentialNode?.status, onChanged])
  const previousUpdateState = useRef<Record<string, string>>({})
  useEffect(() => {
    for (const node of nodes) {
      const previous = previousUpdateState.current[node.id]
      const current = node.update_state ?? "idle"
      if (previous && previous !== current) {
        if (current === "completed") notify({ tone: "success", text: `Нода «${node.name}» обновлена до ${node.agent_version || releaseVersion}` }, 4500)
        else if (current === "error") notify({ tone: "danger", text: `Не удалось обновить «${node.name}»: ${node.update_error || "ошибка обновления"}` }, 6500)
      }
      previousUpdateState.current[node.id] = current
    }
  }, [nodes, notify])
  useEffect(() => {
    if (!nodes.some((node) => Boolean(node.update_state && updateStageInfo[node.update_state]))) return
    const timer = window.setInterval(() => { void onChanged() }, 2000)
    return () => window.clearInterval(timer)
  }, [nodes, onChanged])
  const create = async () => { const profileID = profiles[0]?.id; if (!profileID) return; setBusy(true); setEnrollError(""); try { const result = await api.createNode(name.trim(), "", profileID); setCredential({ node: result.node, token: result.agent_token }); await onChanged() } catch (reason) { setEnrollError(reason instanceof Error ? reason.message : "Не удалось создать ноду") } finally { setBusy(false) } }
  const beginAdd = () => { setName(""); setCredential(null); setAgentURL(initialAgentURL); setEnrollError(""); setCopied(false); setAdding(true) }
  const copyInstallCommand = async () => {
    if (!installCommand) return
    const fallbackCopy = () => { const fallback = document.createElement("textarea"); fallback.value = installCommand; fallback.style.position = "fixed"; fallback.style.opacity = "0"; document.body.appendChild(fallback); fallback.select(); const copiedOK = document.execCommand("copy"); fallback.remove(); return copiedOK }
    try { if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(installCommand); else if (!fallbackCopy()) return } catch { if (!fallbackCopy()) return }
    setCopied(true); window.setTimeout(() => setCopied(false), 2500)
  }
  const changeEnabled = async (node: NodeInfo) => { const enabling = node.status === "disabled"; if (!enabling) { setConfirmNode({ node, action: "disable" }); return }; await api.setNodeEnabled(node.id, true); await onChanged() }
  return <div className="page"><PageHeader eyebrow="Infrastructure" title="Ноды" description="Подключение, состояние и применение профилей на всех серверах." action={<Button onClick={beginAdd}><Plus data-icon="inline-start" />Добавить ноду</Button>} />
    {nodes.length === 0 && <div className="notice"><ShieldCheck /><div><strong>Добавление одной командой</strong><span>Панель сама определит IPv4 и покажет подключение без ручного обновления страницы.</span></div></div>}
    <Card className="table-card"><div className="node-table">{nodes.map((node) => {
      const locked = node.status === "deleting"
      const assignedProfile = profiles.find((profile) => profile.id === node.profile_id)
      const supportsOneClickUpdate = !isOlderVersion(node.agent_version, "0.1.0-beta.3.3")
      const updateStage = supportsOneClickUpdate && node.update_state ? updateStageInfo[node.update_state] : undefined
      const updating = Boolean(updateStage)
      return <div className={`node-table__row ${node.status === "disabled" ? "node-table__row--disabled" : ""} ${locked ? "node-table__row--deleting" : ""}`} key={node.id}>
        <button type="button" className="node-name node-name--button" onClick={() => setSelectedNode(node)}><div className={`node-avatar node-avatar--${nodeVisualState(node)}`}><Server /></div><div><strong>{node.name}</strong><span className="mono">{node.observed_address || node.ingress_address || "IP определится при подключении"} · {node.agent_version || "ожидает агента"}</span><small>{node.status === "online" && node.online_since ? `В сети ${formatDuration(Date.now() - new Date(node.online_since).getTime())}` : node.status === "deleting" ? "Ожидаем очистку конфигурации на VPS" : node.last_seen_at ? `Последний ответ ${formatRelative(node.last_seen_at)}` : "Heartbeat ещё не получен"}</small><NodeMetricsStrip node={node} /></div></button>
        <div className="compact-select"><span>Профиль</span>{locked ? <strong className="node-locked">Удаление…</strong> : <SelectMenu compact label={`Профиль ноды ${node.name}`} value={node.profile_id} onChange={async (value) => { await api.assignProfile(node.id, value); await onChanged() }} options={profiles.map((profile) => ({ value: profile.id, label: profile.name, description: profile.version }))} />}</div>
        <div className="node-actions"><div className="revision-state"><span>{applyStateLabel(node)}</span><span>{assignedProfile?.version || "версия неизвестна"}</span>{node.update_state === "error" && <span className="revision-error" title={node.update_error}>{node.update_error}</span>}{!updating && isOlderVersion(node.agent_version, releaseVersion) && node.status === "online" && (supportsOneClickUpdate ? <Button variant="secondary" className="node-update-button" onClick={() => setConfirmNode({ node, action: "update" })}><RefreshCw />Обновить до {releaseVersion}</Button> : <span className="node-update-legacy" title="В версиях до beta.3.3 валидатор ошибочно отклоняет имя beta-релиза">Первое обновление — вручную</span>)}{node.last_error && <span className="revision-error" title={node.last_error}>{node.last_error}</span>}</div>{!locked && <div className={`node-action-buttons ${node.id === "local" ? "node-action-buttons--local" : ""}`}><Button variant="ghost" className="icon-button" title="Изменить" aria-label={`Изменить ${node.name}`} onClick={() => { setEditingNode(node); setEditName(node.name) }}><Pencil /></Button><Button variant="ghost" className="icon-button" title={node.status === "disabled" ? "Включить" : "Выключить"} aria-label={`${node.status === "disabled" ? "Включить" : "Выключить"} ${node.name}`} onClick={() => { void changeEnabled(node) }}><Power /></Button>{node.id !== "local" && <Button variant="ghost" className="icon-button danger-hover" title="Удалить" aria-label={`Удалить ${node.name}`} onClick={() => setConfirmNode({ node, action: "delete" })}><Trash2 /></Button>}</div>}</div>
        {updateStage && <div className="update-progress" role="progressbar" aria-valuenow={updateStage.percent} aria-valuemin={0} aria-valuemax={100} aria-label={`Обновление ${node.name}: ${updateStage.label}`}><RefreshCw className="spin" /><div className="update-progress__track"><div className="update-progress__fill" style={{ width: `${updateStage.percent}%` }} /></div><span className="update-progress__label">{updateStage.label}</span></div>}
      </div>
    })}</div></Card>
    {adding && <Dialog title={credential ? `Подключение · ${credential.node.name}` : "Новая нода"} description={credential ? "Выполните команду на VPS — статус обновится автоматически." : "Шаг 1 из 2 · задайте понятное название сервера."} onClose={() => { setAdding(false); setCredential(null) }}>
      {credential ? <>
        <div className="dialog__body node-enrollment">
          <Field label="Адрес API нод" hint="Адрес должен быть доступен с новой VPS; по умолчанию используется отдельный порт агентов" error={agentURL && !agentURLValid ? "Укажите полный адрес с http:// или https://" : ""}><Input className="input mono" value={agentURL} onChange={(event) => { setAgentURL(event.target.value); setCopied(false) }} /></Field>
          <div><span className="field__label">Команда установки</span><div className="command-line mono" tabIndex={0} title="Прокрутите строку по горизонтали"><code>{installCommand}</code></div><Button className="copy-command" variant="secondary" disabled={!installCommand} onClick={() => { void copyInstallCommand() }}><Copy data-icon="inline-start" />{copied ? "Скопировано" : "Копировать команду"}</Button></div>
          <div className={`connection-state ${liveCredentialNode?.status === "online" && !liveCredentialNode?.last_error ? "connection-state--ready" : liveCredentialNode?.last_error ? "connection-state--error" : ""}`}>
            {liveCredentialNode?.status === "online" && !liveCredentialNode?.last_error ? <div className="connection-success"><CheckCircle2 /><div><strong>Нода подключена</strong><span>{liveCredentialNode.observed_address || "IPv4 определён"} · профиль применён</span></div></div> : liveCredentialNode?.last_error ? <div className="connection-success"><Activity /><div><strong>Нода подключена, но конфигурация не применилась</strong><span>{liveCredentialNode.last_error}</span></div></div> : <div className="connection-wait"><div className="connection-route" aria-hidden="true"><span className="connection-endpoint"><Server /></span><span className="connection-track"><i /><i /></span><span className="connection-endpoint"><ShieldCheck /></span></div><div><strong>Ожидаем подключение ноды</strong><span>Завершите установку на VPS — статус обновится автоматически.</span></div></div>}
          </div>
        </div>
        <div className="dialog__footer"><Button variant="secondary" onClick={() => { if (liveCredentialNode?.status === "online") setSelectedNode(liveCredentialNode); setAdding(false); setCredential(null) }}>{liveCredentialNode?.status === "online" ? "Открыть ноду" : "Закрыть"}</Button></div>
      </> : <>
        <div className="dialog__body form-stack"><Field label="Название ноды" hint="Например: server-1" error={enrollError}><Input value={name} placeholder="server-1" onChange={(event) => { setName(event.target.value); setEnrollError("") }} autoFocus /></Field><div className="notice"><Server /><div><strong>Профиль можно выбрать после подключения</strong><span>Сначала назначим первый доступный профиль, затем его можно сменить в списке нод.</span></div></div></div>
        <div className="dialog__footer"><Button variant="ghost" onClick={() => setAdding(false)}>Отмена</Button><Button disabled={busy || !name.trim() || profiles.length === 0} onClick={create}>{busy ? "Создаю…" : "Далее"}</Button></div>
      </>}
    </Dialog>}
    {editingNode && <Dialog title={`Редактирование · ${editingNode.name}`} description="Измените отображаемое имя ноды." onClose={() => setEditingNode(null)}><div className="dialog__body form-stack"><Field label="Название ноды"><Input value={editName} onChange={(event) => setEditName(event.target.value)} autoFocus /></Field><div className="notice"><Network /><div><strong>IPv4 определяется автоматически</strong><span className="mono">{editingNode.observed_address || editingNode.ingress_address || "Появится после первого heartbeat"}</span></div></div></div><div className="dialog__footer"><Button variant="ghost" onClick={() => setEditingNode(null)}>Отмена</Button><Button disabled={busy || !editName.trim()} onClick={async () => { setBusy(true); try { await api.updateNode(editingNode.id, editName.trim(), editingNode.ingress_address); setEditingNode(null); await onChanged() } finally { setBusy(false) } }}><Save data-icon="inline-start" />{busy ? "Сохраняю…" : "Сохранить"}</Button></div></Dialog>}
    {confirmNode && <ConfirmDialog title={confirmNode.action === "delete" ? "Удалить ноду?" : confirmNode.action === "update" ? "Обновить агент ноды?" : "Выключить ноду?"} description={confirmNode.action === "delete" ? `EzhikLB очистит свои маршруты на «${confirmNode.node.name}», остановит агент и удалит ноду из панели.` : confirmNode.action === "update" ? `Нода «${confirmNode.node.name}» проверит официальный релиз ${releaseVersion}, заменит агент и автоматически перезапустит его. Маршруты IPVS продолжат работать.` : `Нода «${confirmNode.node.name}» перестанет принимать новые настройки панели. Текущая балансировка продолжит работать.`} confirmLabel={confirmNode.action === "delete" ? "Удалить ноду" : confirmNode.action === "update" ? "Обновить ноду" : "Выключить"} danger={confirmNode.action === "delete"} busy={busy} onCancel={() => setConfirmNode(null)} onConfirm={async () => { setBusy(true); try { if (confirmNode.action === "delete") await api.deleteNode(confirmNode.node.id); else if (confirmNode.action === "update") await api.requestNodeUpdate(confirmNode.node.id); else await api.setNodeEnabled(confirmNode.node.id, false); setConfirmNode(null); await onChanged() } catch (reason) { notify({ tone: "danger", text: reason instanceof Error ? reason.message : "Не удалось выполнить действие" }, 6000) } finally { setBusy(false) } }} />}
    {toast && <ToastNotice tone={toast.tone} text={toast.text} onClose={() => setToast(null)} />}
    {selectedNode && <NodeDetails node={nodes.find((node) => node.id === selectedNode.id) ?? selectedNode} profile={profiles.find((profile) => profile.id === selectedNode.profile_id)} stats={stats.filter((item) => item.node_id === selectedNode.id)} health={health.filter((item) => item.node_id === selectedNode.id)} onClose={() => setSelectedNode(null)} />}
  </div>
}

function ToastNotice({ tone, text, onClose }: { tone: "success" | "danger"; text: string; onClose: () => void }) {
  const Icon = tone === "success" ? CheckCircle2 : CircleAlert
  return createPortal(<div className="toast-layer"><aside className={`toast toast--${tone}`} role={tone === "danger" ? "alert" : "status"} aria-live={tone === "danger" ? "assertive" : "polite"}>
    <span className="toast__icon" aria-hidden="true"><Icon /></span>
    <div className="toast__content"><strong>{tone === "success" ? "Обновление завершено" : "Действие не выполнено"}</strong><span>{text}</span></div>
    <button type="button" className="toast__close" aria-label="Закрыть уведомление" onClick={onClose}><X /></button>
  </aside></div>, document.body)
}

function NodeDetails({ node, profile, stats, health, onClose }: { node: NodeInfo; profile?: Profile; stats: ServiceStat[]; health: BackendHealth[]; onClose: () => void }) {
  const services = stats.filter((item) => !item.backend_address)
  return <Dialog wide title={node.name} description="Текущее состояние агента, конфигурации и маршрутов ноды." onClose={onClose}><div className="dialog__body node-details"><div className="node-detail-summary"><Card><span>Состояние</span><strong>{nodeStatusLabel(node.status)}</strong></Card><Card><span>IPv4</span><strong className="mono">{node.observed_address || node.ingress_address || "—"}</strong></Card><Card><span>Uptime связи</span><strong>{node.status === "online" && node.online_since ? formatDuration(Date.now() - new Date(node.online_since).getTime()) : "—"}</strong></Card><Card><span>Версия агента</span><strong className="mono">{node.agent_version || "—"}</strong></Card></div>{node.metrics && <div className="node-detail-metrics"><Card><Users /><span>Активные IP</span><strong>{formatNumber(node.metrics.active_ips)}</strong><small>за последнюю минуту</small></Card><Card><MemoryStick /><span>RAM</span><strong>{formatPercent(node.metrics.ram_used_percent)}</strong><div className="detail-meter"><i style={{ width: `${Math.min(100, node.metrics.ram_used_percent)}%` }} /></div></Card><Card><Cpu /><span>CPU</span><strong>{formatPercent(node.metrics.cpu_used_percent)}</strong><small>load {node.metrics.load_1.toFixed(2)} / {node.metrics.cpu_cores} vCPU</small></Card><Card><Network /><span>Сеть</span><strong><ArrowDown />{formatNetworkRate(node.metrics.network_rx_bps)}</strong><small><ArrowUp />{formatNetworkRate(node.metrics.network_tx_bps)}</small></Card></div>}<div className="node-detail-grid"><Card><p className="eyebrow">Применение</p><h3>{applyStateLabel(node)}</h3><span>Версия: {profile?.version || "—"}</span><span>Профиль: {profile?.name || "не назначен"}</span><span>{node.last_seen_at ? `Последний heartbeat ${formatRelative(node.last_seen_at)}` : "Heartbeat ещё не получен"}</span></Card><Card><p className="eyebrow">Health-check</p><h3>{health.filter((item) => item.state === "reachable").length} из {health.length} доступны</h3><div className="node-health-list">{health.length === 0 ? <span>Результатов пока нет</span> : health.map((item) => <Badge key={item.address} tone={item.state === "reachable" ? "success" : item.state === "unreachable" ? "danger" : "neutral"}>{item.address} · {healthStateLabel(item.state)}</Badge>)}</div></Card>{node.diagnostics && <Card><p className="eyebrow">Диагностика</p><h3>{node.diagnostics.ipvs_available && node.diagnostics.firewall_ready ? "Всё в порядке" : "Есть проблемы"}</h3><div className="node-health-list"><Badge tone={node.diagnostics.ipvs_available ? "success" : "danger"}>IPVS {node.diagnostics.ipvs_available ? "доступен" : "недоступен"}</Badge><Badge tone={node.diagnostics.firewall_ready ? "success" : "danger"}>Firewall {node.diagnostics.firewall_ready ? "готов" : "не готов"}</Badge></div><span>{node.diagnostics.service_count} служб · {node.diagnostics.destination_count} выходов</span><span>Проверено {formatRelative(node.diagnostics.checked_at)}</span>{node.diagnostics.error && <span className="revision-error" title={node.diagnostics.error}>{node.diagnostics.error}</span>}</Card>}</div>{node.last_error && <div className="validation-error" role="alert"><strong>Ошибка применения:</strong> {node.last_error}</div>}<Card className="node-routes"><div className="panel-card__header"><div><p className="eyebrow">Live IPVS</p><h2>{services.length} маршрутов</h2></div></div>{stats.length === 0 ? <div className="inline-empty"><span>Маршруты появятся после первого heartbeat со статистикой.</span></div> : <div className="traffic-table"><div className="traffic-table__head"><span>Маршрут</span><span>Соединения</span><span>Пакеты</span><span>Трафик</span></div>{stats.map((item) => <div className={`traffic-table__row ${item.backend_address ? "traffic-table__row--backend" : ""}`} key={`${item.protocol}-${item.listen_address}-${item.listen_port}-${item.backend_address}-${item.backend_port}`}><div><Badge>{item.protocol.toUpperCase()}</Badge><span className="mono">{item.backend_address ? `↳ ${item.backend_address}:${item.backend_port}` : `${item.listen_address}:${item.listen_port}`}</span></div><strong>{formatNumber(item.connections)}</strong><strong>{formatNumber(item.incoming_packets)}</strong><strong>{formatBytes(item.incoming_bytes)}</strong></div>)}</div>}</Card></div><div className="dialog__footer"><Button onClick={onClose}>Готово</Button></div></Dialog>
}

function Health({ items, nodes }: { items: BackendHealth[]; nodes: NodeInfo[] }) {
  const reachable = items.filter((item) => item.state === "reachable").length
  const [probing, setProbing] = useState(false)
  const requestProbe = async () => { setProbing(true); try { await Promise.all(nodes.filter((node) => node.status !== "disabled").map((node) => api.requestHealthProbe(node.id))); window.setTimeout(() => setProbing(false), 6000) } catch { setProbing(false) } }
  return <div className="page"><PageHeader eyebrow="ICMP monitoring" title="Health" description="Проверяется доступность хоста. Состояние TCP/UDP-приложения на порту не анализируется." action={<Button variant="secondary" disabled={probing || nodes.length === 0} onClick={requestProbe}><RefreshCw className={probing ? "spin" : ""} data-icon="inline-start" />{probing ? "Проверяю на нодах…" : "Проверить сейчас"}</Button>} />
    <div className="metrics-grid health-metrics"><Metric label="Доступны" value={String(reachable)} icon={<HeartPulse />} tone="success" /><Metric label="Недоступны" value={String(items.filter((item) => item.state === "unreachable").length)} icon={<Activity />} /><Metric label="Всего хостов" value={String(items.length)} icon={<Server />} /></div>
    <Card className="table-card">{items.length === 0 ? <EmptyState icon={<HeartPulse />} title="Ожидаю первый health-check" description="Результаты появятся после применения профиля с хотя бы одним backend." /> : <div className="health-table">{items.map((item) => <div className="health-table__row" key={`${item.node_id}-${item.address}`}><div><strong className="mono">{item.address}</strong><span>{nodes.find((node) => node.id === item.node_id)?.name ?? item.node_id}</span></div><Badge tone={item.state === "reachable" ? "success" : item.state === "unreachable" ? "danger" : "neutral"}>{healthStateLabel(item.state)}</Badge><div><span>Задержка</span><strong className="mono">{item.latency_millis ? `${item.latency_millis} мс` : "—"}</strong></div><div><span>Серия</span><strong className="mono">{item.state === "unreachable" ? `${item.consecutive_failures} ошибок` : `${item.consecutive_successes} успешно`}</strong></div><time>{formatRelative(item.checked_at)}</time></div>)}</div>}</Card>
  </div>
}

function Events({ nodes, profiles }: { nodes: NodeInfo[]; profiles: Profile[] }) {
  const [filter, setFilter] = useState("all")
  const [items, setItems] = useState<AuditEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  useEffect(() => { setLoading(true); setError(""); void api.events(filter).then(setItems).catch((reason) => setError(reason instanceof Error ? reason.message : "Не удалось загрузить журнал")).finally(() => setLoading(false)) }, [filter])
  const filters = [["all", "Все"], ["nodes", "Ноды"], ["profiles", "Профили"], ["errors", "Ошибки"]] as const
  const targetName = (item: AuditEvent) => { if (item.target_type === "node") return nodes.find((node) => node.id === item.target_id)?.name; if (item.target_type === "profile") return profiles.find((profile) => profile.id === item.target_id)?.name; return undefined }
  return <div className="page"><PageHeader eyebrow="Audit log" title="Журнал событий" description="Действия панели и нод за последние 14 дней." />
    <div className="event-filters" role="group" aria-label="Фильтр журнала">{filters.map(([value, label]) => <Button key={value} variant={filter === value ? "primary" : "secondary"} onClick={() => setFilter(value)}>{label}</Button>)}</div>
    {error && <div className="validation-error" role="alert">{error}</div>}
    <Card className="event-card">{loading ? <div className="inline-empty"><LoaderCircle className="spin" /><span>Загружаем события…</span></div> : items.length === 0 ? <EmptyState icon={<ScrollText />} title="Событий пока нет" description="Здесь появятся публикации профилей, подключения нод и ошибки применения." /> : <div className="event-list">{items.map((item) => <div className="event-row" key={item.id}><div className={`event-icon ${item.action.includes("failed") || item.action.includes("error") ? "event-icon--error" : ""}`}><Activity /></div><div><strong>{eventLabel(item.action)}</strong><span>{eventDetails(item, targetName(item))}</span></div><time title={new Date(item.created_at).toLocaleString("ru-RU")}>{formatRelative(item.created_at)}</time></div>)}</div>}</Card>
  </div>
}

function SettingsPage({ current }: { current: SystemSettings }) {
  const [panelPort, setPanelPort] = useState(current.panel_port)
  const [agentPort, setAgentPort] = useState(current.agent_port)
  const [busy, setBusy] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [error, setError] = useState("")
  useEffect(() => {
    if (busy || restarting) return
    setPanelPort(current.panel_port)
    setAgentPort(current.agent_port)
  }, [current.panel_port, current.agent_port, busy, restarting])
  const valid = panelPort >= 1024 && panelPort <= 65535 && agentPort >= 1024 && agentPort <= 65535 && panelPort !== agentPort
  const save = async () => {
    if (!valid) return
    setBusy(true); setError("")
    try {
      await api.updateSettings({ panel_port: panelPort, agent_port: agentPort })
      setRestarting(true)
      const target = new URL(location.href)
      target.port = String(panelPort)
      target.hash = "settings"
      window.setTimeout(() => location.assign(target.toString()), 5000)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Не удалось сохранить настройки")
      setBusy(false)
    }
  }
  return <div className="page"><PageHeader eyebrow="Control plane" title="Настройки" description="Сетевые параметры панели и канала связи с агентами." />
    <Card className="settings-network-card"><div className="settings-card__heading"><div><h2>Порты EzhikLB</h2><p>Web-интерфейс и агенты работают на отдельных HTTP-сокетах.</p></div><Network /></div><div className="form-grid"><Field label="Порт web-панели" hint="1024–65535; после изменения браузер автоматически откроет новый адрес"><Input type="number" min={1024} max={65535} value={panelPort} onChange={(event) => setPanelPort(Number(event.target.value))} /></Field><Field label="Порт API нод" hint="1024–65535; откройте его в firewall для новых подключений"><Input type="number" min={1024} max={65535} value={agentPort} onChange={(event) => setAgentPort(Number(event.target.value))} /></Field></div>{panelPort === agentPort && <div className="validation-error" role="alert">Порты панели и агентов должны отличаться.</div>}{!valid && panelPort !== agentPort && <div className="validation-error" role="alert">Используйте порты от 1024 до 65535.</div>}<div className="settings-port-map"><div><CircleGauge /><span>Web-панель</span><strong className="mono">:{panelPort || "—"}</strong></div><ChevronRight /><div><Server /><span>API агентов</span><strong className="mono">:{agentPort || "—"}</strong></div></div><div className="notice"><ShieldCheck /><div><strong>Существующие ноды не потеряются</strong><span>Сразу предыдущие порты панели и API остаются agent-only точками миграции. Админ-панель на них не публикуется, а новые команды сразу используют новый API-порт.</span></div></div>{error && <div className="validation-error" role="alert">{error}</div>}<div className="settings-actions"><Button disabled={!valid || busy || restarting} onClick={() => { void save() }}>{restarting ? <LoaderCircle className="spin" data-icon="inline-start" /> : <Save data-icon="inline-start" />}{restarting ? "Панель перезапускается…" : busy ? "Сохраняю…" : "Сохранить и перезапустить"}</Button>{restarting && <span><Clock3 />Переходим на новый адрес…</span>}</div></Card>
  </div>
}

function ProfileDialog({ existing, health, nodes, onClose, onSaved }: { existing: { profile: Profile; revision: Revision } | null; health: BackendHealth[]; nodes: NodeInfo[]; onClose: () => void; onSaved: () => Promise<void> }) {
  const nodeAddresses = Array.from(new Set(nodes.flatMap((node) => [node.ingress_address, node.observed_address]).filter(Boolean)))
  const initialName = existing?.profile.name ?? "Новый профиль"
  const initialDescription = existing?.profile.description ?? ""
  const initialConfig = existing?.revision.config ?? emptyConfig()
  const [name, setName] = useState(initialName)
  const [description, setDescription] = useState(initialDescription)
  const [config, setConfig] = useState<ProfileConfig>(initialConfig)
  const [autoVersion, setAutoVersion] = useState(existing?.profile.auto_version ?? true)
  const [version, setVersion] = useState(existing?.profile.version ?? "v1")
  const [revisions, setRevisions] = useState<Revision[]>([])
  const [showHistory, setShowHistory] = useState(false)
  const [busy, setBusy] = useState(false); const [error, setError] = useState("")
  const [confirmClose, setConfirmClose] = useState(false)
  const [rollbackTarget, setRollbackTarget] = useState<Revision | null>(null)
  useEffect(() => { if (existing) void api.revisions(existing.profile.id).then(setRevisions).catch(() => setRevisions([])) }, [existing])
  const nextAutoVersion = `v${(existing?.profile.current_revision ?? 0) + 1}`
  const manualVersionValid = /^[A-Za-z0-9][A-Za-z0-9.-]{0,63}$/.test(version) && (!existing || version !== existing.profile.version)
  const dirty = name !== initialName || description !== initialDescription || autoVersion !== (existing?.profile.auto_version ?? true) || version !== (existing?.profile.version ?? "v1") || JSON.stringify(config) !== JSON.stringify(initialConfig)
  const close = () => { if (!dirty) { onClose(); return } setConfirmClose(true) }
  const save = async () => { setBusy(true); setError(""); try { if (existing) await api.publishProfile(existing.profile.id, name, description, config, autoVersion, autoVersion ? "" : version); else await api.createProfile(name, description, config, autoVersion, autoVersion ? "" : version); await onSaved() } catch (reason) { setError(reason instanceof Error ? reason.message : "Не удалось сохранить профиль") } finally { setBusy(false) } }
  const rollback = async () => { if (!existing || !rollbackTarget) return; setBusy(true); try { await api.rollbackProfile(existing.profile.id, rollbackTarget.number); setRollbackTarget(null); await onSaved() } catch (reason) { setError(reason instanceof Error ? reason.message : "Не удалось выполнить откат") } finally { setBusy(false) } }
  return <Dialog wide title={existing ? `Редактирование · ${existing.profile.name}` : "Новый профиль"} description={existing ? `Следующая версия: ${autoVersion ? nextAutoVersion : version || "укажите версию"}` : "Настройте версию, health-check и маршруты."} onClose={close}><div className="dialog__body">{existing && <div className="revision-toolbar"><Button variant="secondary" onClick={() => setShowHistory((value) => !value)}><History data-icon="inline-start" />История версий</Button><span>Текущая: {existing.profile.version}</span></div>}{showHistory && <div className="revision-list">{revisions.map((revision) => <div key={revision.id}><div><strong>{revision.version}</strong><span>{new Date(revision.created_at).toLocaleString("ru-RU")} · {revisionDiff(revision.config, initialConfig)}</span></div>{revision.number !== existing?.profile.current_revision && <Button variant="secondary" disabled={busy} onClick={() => setRollbackTarget(revision)}>Восстановить</Button>}</div>)}</div>}<div className="profile-basics"><Field label="Название"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field><Field label="Описание"><Input value={description} onChange={(e) => setDescription(e.target.value)} /></Field></div><div className="profile-versioning"><div className="profile-versioning__toggle"><Switch checked={autoVersion} onChange={(checked) => { setAutoVersion(checked); if (!checked && existing) setVersion(existing.profile.version) }} label="Автоматические версии профиля" /><div><strong>Автоматические версии</strong><span>При каждой публикации EzhikLB создаёт v1, v2, v3 и далее.</span></div></div><Field label="Версия" hint={autoVersion ? "Версия будет назначена автоматически" : "Английские буквы, цифры, точки и дефисы"} error={!autoVersion && version && !manualVersionValid ? existing && version === existing.profile.version ? "Измените версию перед публикацией" : "Недопустимый формат версии" : ""}><Input value={autoVersion ? nextAutoVersion : version} disabled={autoVersion} onChange={(event) => setVersion(event.target.value)} /></Field></div><ProfileEditor initial={config} health={health} nodeAddresses={nodeAddresses} onChange={setConfig} />{error && <div className="validation-error" role="alert">{error}</div>}</div><div className="dialog__footer"><Button variant="ghost" onClick={close}>Отмена</Button><Button disabled={busy || !name.trim() || (!autoVersion && !manualVersionValid)} onClick={save}><Save data-icon="inline-start" />{busy ? "Публикую…" : `Опубликовать ${autoVersion ? nextAutoVersion : version}`}</Button></div>
    {confirmClose && <ConfirmDialog title="Закрыть без сохранения?" description="Профиль закроется, а неопубликованные изменения будут потеряны." confirmLabel="Закрыть без сохранения" danger onCancel={() => setConfirmClose(false)} onConfirm={() => { setConfirmClose(false); onClose() }} />}
    {rollbackTarget && <ConfirmDialog title="Восстановить версию?" description={`Вернуть конфигурацию ${rollbackTarget.version}? Будет создана новая версия поверх текущей.`} confirmLabel="Восстановить" busy={busy} onCancel={() => setRollbackTarget(null)} onConfirm={rollback} />}
  </Dialog>
}

function PageHeader({ eyebrow, title, description, action }: { eyebrow: string; title: string; description: string; action?: React.ReactNode }) { return <header className="page-header"><div><p className="eyebrow">{eyebrow}</p><h1>{title}</h1><p>{description}</p></div>{action}</header> }
function Metric({ label, value, icon, tone }: { label: string; value: string; icon: React.ReactNode; tone?: string }) { return <Card className="metric-card"><div className={`metric-card__icon ${tone ? `metric-card__icon--${tone}` : ""}`}>{icon}</div><div><span>{label}</span><strong>{value}</strong></div></Card> }
function NodeRow({ node, profile }: { node: NodeInfo; profile?: Profile }) { const applyLabel = applyStateLabel(node); const applyTone: "danger" | "success" | "warning" | "neutral" = applyLabel === "ошибка применения" ? "danger" : applyLabel === "конфигурация актуальна" && node.status === "online" ? "success" : applyLabel === "применяется" || node.status === "connecting" || node.status === "deleting" ? "warning" : "neutral"; return <div className="node-row"><div className="node-name"><div className={`node-avatar node-avatar--${nodeVisualState(node)}`}><Server /></div><div><strong>{node.name}</strong><span>{node.last_error || `${profile?.name || "Без профиля"} · ${profile?.version || "—"}`}</span><NodeMetricsStrip node={node} compact /></div></div><div className="node-row__revision"><Badge tone={applyTone}>{applyLabel}</Badge></div></div> }
function NodeMetricsStrip({ node, compact = false }: { node: NodeInfo; compact?: boolean }) {
  const metrics = node.metrics
  if (!metrics) return null
  return <div className={`node-metrics ${compact ? "node-metrics--compact" : ""}`} aria-label="Нагрузка ноды за последнюю минуту">
    <span title="Уникальные активные IP за минуту"><Users />{formatNumber(metrics.active_ips)}</span>
    <span className="node-meter" title={`RAM ${formatPercent(metrics.ram_used_percent)}`}><MemoryStick /><i><b style={{ width: `${Math.min(100, metrics.ram_used_percent)}%` }} /></i>{formatPercent(metrics.ram_used_percent)}</span>
    <span title={`CPU за минуту; load ${metrics.load_1.toFixed(2)} на ${metrics.cpu_cores} vCPU`}><Cpu />{formatPercent(metrics.cpu_used_percent)}<small>load {metrics.load_1.toFixed(2)}</small></span>
    <span title="Средний входящий трафик за минуту"><ArrowDown />{formatNetworkRate(metrics.network_rx_bps)}</span>
    <span title="Средний исходящий трафик за минуту"><ArrowUp />{formatNetworkRate(metrics.network_tx_bps)}</span>
  </div>
}
type MetricKey = "ram_used_percent" | "cpu_used_percent" | "network_rx_bps" | "network_tx_bps" | "active_ips"
function aggregateMetricHistory(items: NodeMetricPoint[], aggregate: boolean): NodeMetricPoint[] {
  if (!aggregate) return items
  const buckets = new Map<string, { point: NodeMetricPoint; count: number }>()
  for (const item of items) {
    const key = item.collected_at
    const bucket = buckets.get(key) ?? { point: { ...item, node_id: "all", ram_used_percent: 0, cpu_used_percent: 0, load_1: 0, network_rx_bps: 0, network_tx_bps: 0, active_ips: 0 }, count: 0 }
    bucket.count++; bucket.point.ram_used_percent += item.ram_used_percent; bucket.point.cpu_used_percent += item.cpu_used_percent; bucket.point.load_1 += item.load_1; bucket.point.network_rx_bps += item.network_rx_bps; bucket.point.network_tx_bps += item.network_tx_bps; bucket.point.active_ips += item.active_ips; buckets.set(key, bucket)
  }
  return [...buckets.values()].map(({ point, count }) => ({ ...point, ram_used_percent: point.ram_used_percent / count, cpu_used_percent: point.cpu_used_percent / count, load_1: point.load_1 / count })).sort((a, b) => a.collected_at.localeCompare(b.collected_at))
}
function MetricChart({ title, icon, points, series, format }: { title: string; icon: React.ReactNode; points: NodeMetricPoint[]; series: { key: MetricKey; label: string; color: "success" | "warning" | "accent" }[]; format: (value: number) => string }) {
  const width = 560, height = 150, paddingX = 12, paddingTop = 12, paddingBottom = 18
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)
  const observedMax = Math.max(1, ...points.flatMap((point) => series.map((item) => Number(point[item.key]))))
  const max = observedMax * 1.14
  const plotBottom = height - paddingBottom
  const xAt = (index: number) => paddingX + (points.length < 2 ? 0 : index * (width - paddingX * 2) / (points.length - 1))
  const yAt = (key: MetricKey, index: number) => plotBottom - Number(points[index][key]) / max * (plotBottom - paddingTop)
  const pathFor = (key: MetricKey) => points.map((point, index) => `${index ? "L" : "M"}${xAt(index).toFixed(1)},${yAt(key, index).toFixed(1)}`).join(" ")
  const areaFor = (key: MetricKey) => points.length < 2 ? "" : `${pathFor(key)} L${xAt(points.length - 1).toFixed(1)},${plotBottom} L${xAt(0).toFixed(1)},${plotBottom} Z`
  const latest = points.at(-1)
  const index = hoverIndex == null ? null : Math.min(hoverIndex, points.length - 1)
  const hovered = index == null ? null : points[index]
  const headline = hovered ?? latest
  const trackPointer = (event: React.PointerEvent<SVGSVGElement>) => {
    if (points.length < 2) return
    const rect = event.currentTarget.getBoundingClientRect()
    const relativeX = (event.clientX - rect.left) / rect.width * width
    const step = (width - paddingX * 2) / (points.length - 1)
    setHoverIndex(Math.min(points.length - 1, Math.max(0, Math.round((relativeX - paddingX) / step))))
  }
  const tooltipPercent = index == null ? 0 : (xAt(index) / width) * 100
  const tooltipSide = index == null ? "middle" : index / Math.max(1, points.length - 1) < .24 ? "start" : index / Math.max(1, points.length - 1) > .76 ? "end" : "middle"
  const hoveredYs = index == null ? [] : series.map((item) => yAt(item.key, index))
  const topPoint = hoveredYs.length ? Math.min(...hoveredYs) : paddingTop
  const bottomPoint = hoveredYs.length ? Math.max(...hoveredYs) : plotBottom
  const tooltipVertical = plotBottom - bottomPoint >= topPoint - paddingTop ? "below" : "above"
  const tooltipTop = tooltipVertical === "below" ? bottomPoint : topPoint
  return <Card className="metric-chart"><div className="metric-chart__header"><div>{icon}<div><span>{title}</span><strong>{headline ? series.map((item) => `${item.label}: ${format(Number(headline[item.key]))}`).join(" · ") : "Нет данных"}</strong></div></div><div className="metric-chart__legend">{series.map((item) => <span key={item.key}><i className={`chart-color--${item.color}`} />{item.label}</span>)}</div></div><div className="metric-chart__canvas">{points.length < 2 ? <span>График появится после двух минут сбора данных</span> : <>
    <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${title} за последние 24 часа`} preserveAspectRatio="none" onPointerMove={trackPointer} onPointerDown={trackPointer} onPointerLeave={() => setHoverIndex(null)} onPointerUp={() => setHoverIndex(null)} onPointerCancel={() => setHoverIndex(null)}>
      <path className="chart-grid-line" d={`M${paddingX},${(paddingTop + plotBottom) / 2} H${width - paddingX}`} />
      {series.map((item) => <path key={`area-${item.key}`} className={`chart-area chart-area--${item.color}`} d={areaFor(item.key)} />)}
      {series.map((item) => <path key={`${item.key}-${points.length}`} className={`chart-line chart-line--${item.color}`} d={pathFor(item.key)} />)}
      {series.map((item) => <circle key={`live-${item.key}`} className={`chart-live-dot chart-live-dot--${item.color}`} cx={xAt(points.length - 1)} cy={yAt(item.key, points.length - 1)} r={2.8} />)}
      {index != null && <g aria-hidden="true">
        <line className="chart-hover-line" x1={xAt(index)} x2={xAt(index)} y1={paddingTop} y2={plotBottom} />
        {series.map((item) => <circle key={item.key} className={`chart-hover-dot chart-hover-dot--${item.color}`} cx={xAt(index)} cy={yAt(item.key, index)} r={3.5} />)}
      </g>}
    </svg>
    <div className="chart-time-axis" aria-hidden="true"><span>{new Date(points[0].collected_at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })}</span><span>сейчас</span></div>
    {hovered && <div className={`chart-tooltip chart-tooltip--${tooltipSide} chart-tooltip--${tooltipVertical}`} style={{ left: `${tooltipPercent}%`, top: `${tooltipTop / height * 100}%` }} aria-hidden="true">
      <time>{new Date(hovered.collected_at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })}</time>
      {series.map((item) => <div key={item.key}><i className={`chart-color--${item.color}`} /><span>{item.label}</span><strong>{format(Number(hovered[item.key]))}</strong></div>)}
    </div>}
  </>}</div></Card>
}
function Placeholder({ title, description, icon }: { title: string; description: string; icon: React.ReactNode }) { return <div className="page"><PageHeader eyebrow="EzhikLB" title={title} description={description} /><Card><EmptyState icon={icon} title="Раздел готовится" description={description} /></Card></div> }
function formatNumber(value: number) { return new Intl.NumberFormat("ru-RU", { notation: value >= 10000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value) }
function formatBytes(value: number) { if (value < 1024) return `${value} Б`; const units = ["КБ", "МБ", "ГБ", "ТБ"]; let next = value / 1024; let unit = 0; while (next >= 1024 && unit < units.length - 1) { next /= 1024; unit++ } return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 }).format(next)} ${units[unit]}` }
function formatPercent(value: number) { return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 0 }).format(value)}%` }
function formatNetworkRate(bytesPerSecond: number) { const bits = bytesPerSecond * 8; if (bits < 1000) return `${Math.round(bits)} бит/с`; const units = ["Кбит/с", "Мбит/с", "Гбит/с"]; let next = bits / 1000; let unit = 0; while (next >= 1000 && unit < units.length - 1) { next /= 1000; unit++ } return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: next >= 100 ? 0 : 1 }).format(next)} ${units[unit]}` }
function formatRelative(value: string) { const minutes = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 60000)); if (minutes < 1) return "только что"; if (minutes < 60) return `${minutes} мин назад`; const hours = Math.floor(minutes / 60); if (hours < 24) return `${hours} ч назад`; return new Date(value).toLocaleDateString("ru-RU") }
function formatDuration(milliseconds: number) { const seconds = Math.max(0, Math.floor(milliseconds / 1000)); if (seconds < 60) return `${seconds} сек`; const minutes = Math.floor(seconds / 60); if (minutes < 60) return `${minutes} мин`; const hours = Math.floor(minutes / 60); if (hours < 24) return `${hours} ч ${minutes % 60} мин`; const days = Math.floor(hours / 24); return `${days} д ${hours % 24} ч` }
function eventLabel(action: string) { return ({ "profile.created": "Профиль создан", "profile.published": "Профиль опубликован", "profile.rolled_back": "Профиль восстановлен", "profile.deleted": "Профиль удалён", "node.created": "Нода создана", "node.updated": "Нода изменена", "node.profile_assigned": "Профиль назначен ноде", "node.enabled_changed": "Состояние ноды изменено", "node.decommission_requested": "Запрошено удаление ноды", "node.decommissioned": "Нода очищена и удалена", "node.apply_failed": "Ошибка применения конфигурации", "node.apply_recovered": "Конфигурация снова применена", "node.credential_rotated": "Токен ноды обновлён", "node.credential_revoked": "Токен ноды отозван", "node.health_probe_requested": "Запрошена внеплановая проверка", "node.update_requested": "Запрошено обновление агента", "settings.updated": "Сетевые настройки изменены" } as Record<string, string>)[action] ?? action }
function eventDetails(item: AuditEvent, targetName?: string) { try { const details = JSON.parse(item.details) as Record<string, unknown>; const name = typeof details.name === "string" ? details.name : targetName ?? ""; const version = typeof details.version === "string" ? details.version : ""; const error = typeof details.error === "string" ? details.error : ""; if (error) return name ? `${name} · ${error}` : error; if (name && version) return `${name} · ${version}`; if (name || version) return name || version } catch { /* Keep the stable target ID for legacy events. */ } return targetName || item.target_id }
function nodeVisualState(node: NodeInfo) { if (node.status === "disabled") return "disabled"; if (node.status === "connecting" || node.status === "deleting" || node.apply_state === "applying" || node.desired_revision !== node.applied_revision) return "applying"; if (node.status === "online") return "online"; return "offline" }
function healthStateLabel(state: BackendHealth["state"]) { return ({ reachable: "Доступен", unreachable: "Недоступен", unknown: "Нет данных" } as const)[state] }
function nodeStatusLabel(status?: NodeInfo["status"]) { return ({ connecting: "подключается", online: "online", offline: "offline", error: "ошибка", disabled: "выключена", deleting: "удаляется" } as const)[status ?? "offline"] }
function applyStateLabel(node: NodeInfo) { if (node.status === "deleting" || node.apply_state === "decommissioning") return "очистка и остановка"; if (node.status === "disabled") return "выключена"; if (node.apply_state === "error" || node.last_error) return "ошибка применения"; if (node.apply_state === "applying" || node.desired_revision !== node.applied_revision) return "применяется"; if (node.apply_state === "applied") return "конфигурация актуальна"; return "ожидает конфигурацию" }
function isOlderVersion(current: string | undefined, target: string): boolean {
  if (!current) return false
  const parse = (value: string) => {
    const [core, ...prereleaseParts] = value.replace(/^v/i, "").split("-")
    const [major, minor, patch] = core.split(".").map((part) => Number(part) || 0)
    const segments = prereleaseParts.join("-").split(".").filter(Boolean)
    const channel = segments[0] ?? ""
    const channelNumbers = segments.slice(1).map((part) => Number(part) || 0)
    return { major, minor, patch, channel, channelNumbers }
  }
  const a = parse(current), b = parse(target)
  if (a.major !== b.major) return a.major < b.major
  if (a.minor !== b.minor) return a.minor < b.minor
  if (a.patch !== b.patch) return a.patch < b.patch
  const rank: Record<string, number> = { "": 3, alpha: 1, beta: 2 }
  const rankOf = (channel: string) => rank[channel] ?? 2
  if (a.channel !== b.channel) return rankOf(a.channel) < rankOf(b.channel)
  const depth = Math.max(a.channelNumbers.length, b.channelNumbers.length)
  for (let i = 0; i < depth; i++) {
    const av = a.channelNumbers[i] ?? 0, bv = b.channelNumbers[i] ?? 0
    if (av !== bv) return av < bv
  }
  return false
}
function revisionDiff(value: ProfileConfig, current: ProfileConfig) { const backends = value.listeners.reduce((sum, listener) => sum + listener.backends.length, 0); if (JSON.stringify(value) === JSON.stringify(current)) return `${value.listeners.length} записей · ${backends} выходов · текущая конфигурация`; const currentBackends = current.listeners.reduce((sum, listener) => sum + listener.backends.length, 0); const listenerDelta = value.listeners.length - current.listeners.length; const backendDelta = backends - currentBackends; const changes = [`${value.listeners.length} записей`, `${backends} выходов`]; if (listenerDelta) changes.push(`${listenerDelta > 0 ? "+" : ""}${listenerDelta} записей`); if (backendDelta) changes.push(`${backendDelta > 0 ? "+" : ""}${backendDelta} выходов`); if (value.health_check.enabled !== current.health_check.enabled) changes.push(`health-check ${value.health_check.enabled ? "включён" : "выключен"}`); return changes.join(" · ") }
