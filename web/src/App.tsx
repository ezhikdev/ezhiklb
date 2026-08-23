import { Activity, Boxes, ChevronRight, CircleGauge, HeartPulse, Hexagon, LogOut, Network, Plus, Save, Server, Settings, ShieldCheck } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { ApiError, api } from "./lib/api"
import type { BackendHealth, NodeInfo, Profile, ProfileConfig, Revision, ServiceStat, Status } from "./types"
import { ProfileEditor } from "./components/ProfileEditor"
import { Badge, Button, Card, Dialog, EmptyState, Field, Input } from "./components/ui"

type Page = "overview" | "profiles" | "nodes" | "health" | "settings"

const nav = [
  ["overview", "Обзор", CircleGauge], ["profiles", "Профили", Boxes], ["nodes", "Ноды", Server],
  ["health", "Health", HeartPulse], ["settings", "Настройки", Settings],
] as const

const emptyConfig = (): ProfileConfig => ({ schema_version: 1, health_check: { enabled: true, interval_seconds: 10, timeout_millis: 1000, failure_threshold: 3, recovery_threshold: 2 }, listeners: [] })

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
  const [error, setError] = useState("")
  const [editing, setEditing] = useState<{ profile: Profile; revision: Revision } | null>(null)
  const [creating, setCreating] = useState(false)

  const load = useCallback(async () => {
    try {
      const [nextStatus, nextProfiles, nextNodes, nextHealth, nextStats] = await Promise.all([api.status(), api.profiles(), api.nodes(), api.health(), api.stats()])
      setStatus(nextStatus); setProfiles(nextProfiles); setNodes(nextNodes); setHealth(nextHealth); setStats(nextStats); setAuthenticated(true); setError("")
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
      <div className="sidebar__footer"><div className="version"><span className="live-dot" />alpha · {status?.version}</div><Button variant="ghost" className="logout" onClick={async () => { await api.logout(); setAuthenticated(false) }}><LogOut data-icon="inline-start" />Выйти</Button></div>
    </aside>
    <main id="main-content" tabIndex={-1} className="main-content">
      {error && <div className="error-banner" role="alert"><span>{error}</span><button onClick={() => setError("")} aria-label="Закрыть">×</button></div>}
      {page === "overview" && <Overview status={status} nodes={nodes} profiles={profiles} stats={stats} navigate={setPage} />}
      {page === "profiles" && <Profiles profiles={profiles} nodes={nodes} onCreate={() => setCreating(true)} onOpen={openProfile} />}
      {page === "nodes" && <Nodes nodes={nodes} profiles={profiles} onChanged={load} />}
      {page === "health" && <Health items={health} nodes={nodes} />}
      {page === "settings" && <Placeholder title="Настройки" description="Глобальные параметры панели и управление обновлениями готовятся к следующей итерации." icon={<Settings />} />}
    </main>
    {(editing || creating) && <ProfileDialog existing={editing} health={health} onClose={() => { setEditing(null); setCreating(false) }} onSaved={async () => { setEditing(null); setCreating(false); await load() }} />}
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
  return <div className="page"><PageHeader eyebrow="Control plane" title="Обзор" description="Состояние инфраструктуры EzhikLB в одном месте." />
    <div className="metrics-grid"><Metric label="Ноды онлайн" value={`${status?.online_nodes ?? 0}/${status?.nodes ?? 0}`} icon={<Server />} tone="success" /><Metric label="Активные записи" value={String(status?.listeners ?? 0)} icon={<Network />} /><Metric label="Входящие пакеты" value={formatNumber(packets)} icon={<Activity />} /><Metric label="Принято трафика" value={formatBytes(bytes)} icon={<CircleGauge />} /></div>
    <div className="overview-grid"><Card className="panel-card"><div className="panel-card__header"><div><p className="eyebrow">Ноды</p><h2>Состояние применения</h2></div><Button variant="ghost" onClick={() => navigate("nodes")}>Все ноды<ChevronRight data-icon="inline-end" /></Button></div><div className="node-list">{nodes.map((node) => <NodeRow key={node.id} node={node} profile={profiles.find((profile) => profile.id === node.profile_id)} />)}</div></Card><Card className="panel-card infrastructure-card"><div className="panel-card__header"><div><p className="eyebrow">Система</p><h2>Контур управления</h2></div></div><div className="flow"><div><ShieldCheck /><strong>Panel</strong><span>desired state</span></div><span className="flow__line" /><div><Activity /><strong>Agent</strong><span>reconcile</span></div><span className="flow__line" /><div><Network /><strong>IPVS</strong><span>TCP + UDP</span></div></div></Card></div>
    <TrafficPanel stats={stats} />
  </div>
}

function TrafficPanel({ stats }: { stats: ServiceStat[] }) {
  return <Card className="traffic-card"><div className="panel-card__header"><div><p className="eyebrow">Live IPVS</p><h2>Фактическое распределение</h2></div><span className="traffic-updated">обновление каждые 15 секунд</span></div>
    {stats.length === 0 ? <EmptyState icon={<Activity />} title="Трафика пока нет" description="Счётчики появятся после применения хотя бы одной записи." /> : <div className="traffic-table">
      <div className="traffic-table__head"><span>Маршрут</span><span>Соединения</span><span>Пакеты</span><span>Входящий трафик</span></div>
      {stats.map((item) => <div className={`traffic-table__row ${item.backend_address ? "traffic-table__row--backend" : ""}`} key={`${item.node_id}-${item.protocol}-${item.listen_address}-${item.listen_port}-${item.backend_address}-${item.backend_port}`}>
        <div><Badge>{item.protocol.toUpperCase()}</Badge><span className="mono">{item.backend_address ? `↳ ${item.backend_address}:${item.backend_port}` : `${item.listen_address}:${item.listen_port}`}</span></div>
        <strong className="mono">{formatNumber(item.connections)}</strong><strong className="mono">{formatNumber(item.incoming_packets)}</strong><strong className="mono">{formatBytes(item.incoming_bytes)}</strong>
      </div>)}
    </div>}
  </Card>
}

function Profiles({ profiles, nodes, onCreate, onOpen }: { profiles: Profile[]; nodes: NodeInfo[]; onCreate: () => void; onOpen: (profile: Profile) => void }) {
  return <div className="page"><PageHeader eyebrow="Desired state" title="Профили" description="Один профиль можно назначить нескольким нодам." action={<Button onClick={onCreate}><Plus data-icon="inline-start" />Новый профиль</Button>} />
    <div className="profile-grid">{profiles.map((profile) => <Card key={profile.id} className="profile-card" onClick={() => onOpen(profile)} role="button" tabIndex={0} onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onOpen(profile)}><div className="profile-card__top"><div className="profile-icon"><Boxes /></div><Badge>rev {profile.current_revision}</Badge></div><div><h2>{profile.name}</h2><p>{profile.description || "Без описания"}</p></div><div className="profile-card__meta"><span>{nodes.filter((node) => node.profile_id === profile.id).length} нод</span><span>Изменён {formatRelative(profile.updated_at)}</span></div></Card>)}</div>
  </div>
}

function Nodes({ nodes, profiles, onChanged }: { nodes: NodeInfo[]; profiles: Profile[]; onChanged: () => Promise<void> }) {
  return <div className="page"><PageHeader eyebrow="Infrastructure" title="Ноды" description="Назначайте каждой ноде один из переиспользуемых профилей." action={<Button disabled title="Будет доступно в следующей alpha"><Plus data-icon="inline-start" />Добавить ноду</Button>} />
    <div className="notice"><Network /><div><strong>Remote nodes скоро</strong><span>В alpha полностью работает локальная нода режима Panel + Node. Безопасное добавление удалённых нод через mTLS появится следующим этапом.</span></div></div>
    <Card className="table-card"><div className="node-table">{nodes.map((node) => <div className="node-table__row" key={node.id}><div className="node-name"><div className="node-avatar"><Server /></div><div><strong>{node.name}</strong><span className="mono">{node.ingress_address || "auto-detect"}</span></div></div><Badge tone={node.status === "online" ? "success" : node.status === "error" ? "danger" : "neutral"}>{node.status}</Badge><label className="compact-select"><span>Профиль</span><select value={node.profile_id} onChange={async (e) => { await api.assignProfile(node.id, e.target.value); await onChanged() }}>{profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label><div className="revision-state"><span>desired {node.desired_revision}</span><span>actual {node.applied_revision}</span></div></div>)}</div></Card>
  </div>
}

function Health({ items, nodes }: { items: BackendHealth[]; nodes: NodeInfo[] }) {
  const reachable = items.filter((item) => item.state === "reachable").length
  return <div className="page"><PageHeader eyebrow="ICMP monitoring" title="Health" description="Проверяется доступность хоста. Состояние TCP/UDP-приложения на порту не анализируется." />
    <div className="metrics-grid health-metrics"><Metric label="Доступны" value={String(reachable)} icon={<HeartPulse />} tone="success" /><Metric label="Недоступны" value={String(items.filter((item) => item.state === "unreachable").length)} icon={<Activity />} /><Metric label="Всего хостов" value={String(items.length)} icon={<Server />} /></div>
    <Card className="table-card">{items.length === 0 ? <EmptyState icon={<HeartPulse />} title="Ожидаю первый health-check" description="Результаты появятся после применения профиля с хотя бы одним backend." /> : <div className="health-table">{items.map((item) => <div className="health-table__row" key={`${item.node_id}-${item.address}`}><div><strong className="mono">{item.address}</strong><span>{nodes.find((node) => node.id === item.node_id)?.name ?? item.node_id}</span></div><Badge tone={item.state === "reachable" ? "success" : item.state === "unreachable" ? "danger" : "neutral"}>{item.state}</Badge><div><span>Latency</span><strong className="mono">{item.latency_millis ? `${item.latency_millis} ms` : "—"}</strong></div><div><span>Серия</span><strong className="mono">{item.state === "unreachable" ? `${item.consecutive_failures} fail` : `${item.consecutive_successes} ok`}</strong></div><time>{formatRelative(item.checked_at)}</time></div>)}</div>}</Card>
  </div>
}

function ProfileDialog({ existing, health, onClose, onSaved }: { existing: { profile: Profile; revision: Revision } | null; health: BackendHealth[]; onClose: () => void; onSaved: () => Promise<void> }) {
  const initialName = existing?.profile.name ?? "Новый профиль"
  const initialDescription = existing?.profile.description ?? ""
  const initialConfig = existing?.revision.config ?? emptyConfig()
  const [name, setName] = useState(initialName)
  const [description, setDescription] = useState(initialDescription)
  const [config, setConfig] = useState<ProfileConfig>(initialConfig)
  const [busy, setBusy] = useState(false); const [error, setError] = useState("")
  const dirty = name !== initialName || description !== initialDescription || JSON.stringify(config) !== JSON.stringify(initialConfig)
  const close = () => { if (!dirty || window.confirm("Закрыть профиль и потерять неопубликованные изменения?")) onClose() }
  const save = async () => { setBusy(true); setError(""); try { if (existing) await api.publishProfile(existing.profile.id, name, description, config); else await api.createProfile(name, description, config); await onSaved() } catch (reason) { setError(reason instanceof Error ? reason.message : "Не удалось сохранить профиль") } finally { setBusy(false) } }
  return <Dialog wide title={existing ? `Редактирование · ${existing.profile.name}` : "Новый профиль"} description={existing ? `Сохранение создаст revision ${existing.profile.current_revision + 1}` : "Настройте health-check и маршруты."} onClose={close}><div className="dialog__body"><div className="profile-basics"><Field label="Название"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field><Field label="Описание"><Input value={description} onChange={(e) => setDescription(e.target.value)} /></Field></div><ProfileEditor initial={config} health={health} onChange={setConfig} />{error && <div className="validation-error" role="alert">{error}</div>}</div><div className="dialog__footer"><Button variant="ghost" onClick={close}>Отмена</Button><Button disabled={busy || !name.trim()} onClick={save}><Save data-icon="inline-start" />{busy ? "Публикую…" : "Опубликовать ревизию"}</Button></div></Dialog>
}

function PageHeader({ eyebrow, title, description, action }: { eyebrow: string; title: string; description: string; action?: React.ReactNode }) { return <header className="page-header"><div><p className="eyebrow">{eyebrow}</p><h1>{title}</h1><p>{description}</p></div>{action}</header> }
function Metric({ label, value, icon, tone }: { label: string; value: string; icon: React.ReactNode; tone?: string }) { return <Card className="metric-card"><div className={`metric-card__icon ${tone ? `metric-card__icon--${tone}` : ""}`}>{icon}</div><div><span>{label}</span><strong>{value}</strong></div></Card> }
function NodeRow({ node, profile }: { node: NodeInfo; profile?: Profile }) { const synced = node.desired_revision === node.applied_revision; return <div className="node-row"><div className="node-name"><div className="node-avatar"><Server /></div><div><strong>{node.name}</strong><span>{node.last_error || profile?.name || "Без профиля"}</span></div></div><div className="node-row__revision"><span className="mono">{node.applied_revision}/{node.desired_revision}</span><Badge tone={node.status === "error" ? "danger" : synced && node.status === "online" ? "success" : "warning"}>{node.status === "error" ? "ошибка" : synced ? "применено" : "применяется"}</Badge></div></div> }
function Placeholder({ title, description, icon }: { title: string; description: string; icon: React.ReactNode }) { return <div className="page"><PageHeader eyebrow="EzhikLB" title={title} description={description} /><Card><EmptyState icon={icon} title="Раздел готовится" description={description} /></Card></div> }
function formatNumber(value: number) { return new Intl.NumberFormat("ru-RU", { notation: value >= 10000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value) }
function formatBytes(value: number) { if (value < 1024) return `${value} Б`; const units = ["КБ", "МБ", "ГБ", "ТБ"]; let next = value / 1024; let unit = 0; while (next >= 1024 && unit < units.length - 1) { next /= 1024; unit++ } return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 }).format(next)} ${units[unit]}` }
function formatRelative(value: string) { const minutes = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 60000)); if (minutes < 1) return "только что"; if (minutes < 60) return `${minutes} мин назад`; const hours = Math.floor(minutes / 60); if (hours < 24) return `${hours} ч назад`; return new Date(value).toLocaleDateString("ru-RU") }
