import { Activity, Boxes, CheckCircle2, ChevronDown, ChevronRight, CircleGauge, Clock3, Copy, HeartPulse, Hexagon, History, LoaderCircle, LogOut, Network, Pencil, Plus, Power, RefreshCw, Save, Server, Settings, ShieldCheck, Trash2 } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { ApiError, api } from "./lib/api"
import type { BackendHealth, NodeInfo, Profile, ProfileConfig, Revision, ServiceStat, Status, SystemSettings } from "./types"
import { ProfileEditor } from "./components/ProfileEditor"
import { Badge, Button, Card, Dialog, EmptyState, Field, Input, SelectMenu } from "./components/ui"

type Page = "overview" | "profiles" | "nodes" | "health" | "settings"

const nav = [
  ["overview", "Обзор", CircleGauge], ["profiles", "Профили", Boxes], ["nodes", "Ноды", Server],
  ["health", "Health", HeartPulse], ["settings", "Настройки", Settings],
] as const

const emptyConfig = (): ProfileConfig => ({ schema_version: 1, health_check: { enabled: true, interval_seconds: 10, timeout_millis: 1000, failure_threshold: 3, recovery_threshold: 2 }, listeners: [] })
const releaseVersion = "0.1.0-alpha.8.2"
const shellArg = (value: string) => `'${value.replace(/'/g, `'"'"'`)}'`

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
      <div className="sidebar__footer"><div className="version"><span className="live-dot" />alpha · {status?.version}</div><Button variant="ghost" className="logout" onClick={async () => { await api.logout(); setAuthenticated(false) }}><LogOut data-icon="inline-start" />Выйти</Button></div>
    </aside>
    <main id="main-content" tabIndex={-1} className="main-content">
      {error && <div className="error-banner" role="alert"><span>{error}</span><button onClick={() => setError("")} aria-label="Закрыть">×</button></div>}
      {page === "overview" && <Overview status={status} nodes={nodes} profiles={profiles} stats={stats} navigate={setPage} />}
      {page === "profiles" && <Profiles profiles={profiles} nodes={nodes} onCreate={() => setCreating(true)} onOpen={openProfile} onChanged={load} />}
      {page === "nodes" && <Nodes nodes={nodes} profiles={profiles} settings={settings} stats={stats} health={health} onChanged={load} />}
      {page === "health" && <Health items={health} nodes={nodes} />}
      {page === "settings" && <SettingsPage current={settings} />}
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
    <TrafficPanel stats={stats} nodes={nodes} />
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
  return <div className="page"><PageHeader eyebrow="Desired state" title="Профили" description="Один профиль можно назначить нескольким нодам." action={<Button onClick={onCreate}><Plus data-icon="inline-start" />Новый профиль</Button>} />
    <div className="profile-grid">{profiles.map((profile) => {
      const assigned = nodes.filter((node) => node.profile_id === profile.id).length
      return <Card key={profile.id} className="profile-card" onClick={() => onOpen(profile)} role="button" tabIndex={0} onKeyDown={(event) => (event.key === "Enter" || event.key === " ") && onOpen(profile)}>
        <div className="profile-card__top"><div className="profile-icon"><Boxes /></div><div className="profile-card__actions"><Badge>rev {profile.current_revision}</Badge><Button variant="ghost" className="icon-button" title="Клонировать профиль" aria-label={`Клонировать ${profile.name}`} onClick={(event) => { event.stopPropagation(); setCloning(profile); setCloneName(`${profile.name} — копия`) }}><Copy /></Button><Button variant="ghost" className="icon-button danger-hover" disabled={assigned > 0} title={assigned ? "Сначала назначьте нодам другой профиль" : "Удалить профиль"} aria-label={`Удалить ${profile.name}`} onClick={async (event) => { event.stopPropagation(); if (!window.confirm(`Удалить профиль «${profile.name}»?`)) return; await api.deleteProfile(profile.id); await onChanged() }}><Trash2 /></Button></div></div>
        <div><h2>{profile.name}</h2><p>{profile.description || "Без описания"}</p></div><div className="profile-card__meta"><span>{assigned} нод</span><span>Изменён {formatRelative(profile.updated_at)}</span></div>
      </Card>
    })}</div>
    {cloning && <Dialog title={`Клонирование · ${cloning.name}`} description="Будет создан независимый профиль с текущей конфигурацией." onClose={() => setCloning(null)}><div className="dialog__body"><Field label="Название копии"><Input value={cloneName} onChange={(event) => setCloneName(event.target.value)} autoFocus /></Field></div><div className="dialog__footer"><Button variant="ghost" onClick={() => setCloning(null)}>Отмена</Button><Button disabled={busy || !cloneName.trim()} onClick={async () => { setBusy(true); try { await api.cloneProfile(cloning.id, cloneName.trim()); setCloning(null); await onChanged() } finally { setBusy(false) } }}><Copy data-icon="inline-start" />{busy ? "Создаю…" : "Создать копию"}</Button></div></Dialog>}
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
  const create = async () => { const profileID = profiles[0]?.id; if (!profileID) return; setBusy(true); setEnrollError(""); try { const result = await api.createNode(name.trim(), "", profileID); setCredential({ node: result.node, token: result.agent_token }); await onChanged() } catch (reason) { setEnrollError(reason instanceof Error ? reason.message : "Не удалось создать ноду") } finally { setBusy(false) } }
  const beginAdd = () => { setName(""); setCredential(null); setAgentURL(initialAgentURL); setEnrollError(""); setCopied(false); setAdding(true) }
  const copyInstallCommand = async () => {
    if (!installCommand) return
    const fallbackCopy = () => { const fallback = document.createElement("textarea"); fallback.value = installCommand; fallback.style.position = "fixed"; fallback.style.opacity = "0"; document.body.appendChild(fallback); fallback.select(); const copiedOK = document.execCommand("copy"); fallback.remove(); return copiedOK }
    try { if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(installCommand); else if (!fallbackCopy()) return } catch { if (!fallbackCopy()) return }
    setCopied(true); window.setTimeout(() => setCopied(false), 2500)
  }
  const changeEnabled = async (node: NodeInfo) => { const enabling = node.status === "disabled"; if (!enabling && !window.confirm(`Выключить ноду «${node.name}»? Балансировка останется в текущем состоянии, но панель перестанет принимать heartbeat.`)) return; await api.setNodeEnabled(node.id, enabling); await onChanged() }
  return <div className="page"><PageHeader eyebrow="Infrastructure" title="Ноды" description="Подключение, состояние и применение профилей на всех серверах." action={<Button onClick={beginAdd}><Plus data-icon="inline-start" />Добавить ноду</Button>} />
    <div className="notice"><ShieldCheck /><div><strong>Добавление одной командой</strong><span>Панель сама определит IPv4 и покажет подключение без ручного обновления страницы.</span></div></div>
    <Card className="table-card"><div className="node-table">{nodes.map((node) => <div className={`node-table__row ${node.status === "disabled" ? "node-table__row--disabled" : ""}`} key={node.id}><button type="button" className="node-name node-name--button" onClick={() => setSelectedNode(node)}><div className="node-avatar"><Server /></div><div><strong>{node.name}</strong><span className="mono">{node.observed_address || node.ingress_address || "IP определится при подключении"} · {node.agent_version || "ожидает агента"}</span><small>{node.status === "online" && node.online_since ? `В сети ${formatDuration(Date.now() - new Date(node.online_since).getTime())}` : node.last_seen_at ? `Последний ответ ${formatRelative(node.last_seen_at)}` : "Heartbeat ещё не получен"}</small></div></button><Badge tone={node.status === "online" ? "success" : node.status === "error" ? "danger" : node.status === "connecting" ? "warning" : "neutral"}>{nodeStatusLabel(node.status)}</Badge><div className="compact-select"><span>Профиль</span><SelectMenu compact label={`Профиль ноды ${node.name}`} value={node.profile_id} onChange={async (value) => { await api.assignProfile(node.id, value); await onChanged() }} options={profiles.map((profile) => ({ value: profile.id, label: profile.name, description: `Revision ${profile.current_revision}` }))} /></div><div className="node-actions"><div className="revision-state"><span>{applyStateLabel(node)}</span><span>revision {node.applied_revision}/{node.desired_revision}</span>{node.last_error && <span className="revision-error" title={node.last_error}>{node.last_error}</span>}</div><Button variant="ghost" className="icon-button" title="Изменить" aria-label={`Изменить ${node.name}`} onClick={() => { setEditingNode(node); setEditName(node.name) }}><Pencil /></Button><Button variant="ghost" className="icon-button" title={node.status === "disabled" ? "Включить" : "Выключить"} aria-label={`${node.status === "disabled" ? "Включить" : "Выключить"} ${node.name}`} onClick={() => { void changeEnabled(node) }}><Power /></Button>{node.id !== "local" && <Button variant="ghost" className="icon-button danger-hover" title="Удалить" aria-label={`Удалить ${node.name}`} onClick={async () => { if (!window.confirm(`Удалить ноду «${node.name}» из панели?`)) return; await api.deleteNode(node.id); await onChanged() }}><Trash2 /></Button>}</div></div>)}</div></Card>
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
    {selectedNode && <NodeDetails node={nodes.find((node) => node.id === selectedNode.id) ?? selectedNode} profile={profiles.find((profile) => profile.id === selectedNode.profile_id)} stats={stats.filter((item) => item.node_id === selectedNode.id)} health={health.filter((item) => item.node_id === selectedNode.id)} onClose={() => setSelectedNode(null)} />}
  </div>
}

function NodeDetails({ node, profile, stats, health, onClose }: { node: NodeInfo; profile?: Profile; stats: ServiceStat[]; health: BackendHealth[]; onClose: () => void }) {
  const services = stats.filter((item) => !item.backend_address)
  return <Dialog wide title={node.name} description="Текущее состояние агента, конфигурации и маршрутов ноды." onClose={onClose}><div className="dialog__body node-details"><div className="node-detail-summary"><Card><span>Состояние</span><Badge tone={node.status === "online" ? "success" : node.status === "error" ? "danger" : node.status === "connecting" ? "warning" : "neutral"}>{nodeStatusLabel(node.status)}</Badge></Card><Card><span>IPv4</span><strong className="mono">{node.observed_address || node.ingress_address || "—"}</strong></Card><Card><span>Uptime связи</span><strong>{node.status === "online" && node.online_since ? formatDuration(Date.now() - new Date(node.online_since).getTime()) : "—"}</strong></Card><Card><span>Версия агента</span><strong className="mono">{node.agent_version || "—"}</strong></Card></div><div className="node-detail-grid"><Card><p className="eyebrow">Применение</p><h3>{applyStateLabel(node)}</h3><span>Revision {node.applied_revision} / {node.desired_revision}</span><span>Профиль: {profile?.name || "не назначен"}</span><span>{node.last_seen_at ? `Последний heartbeat ${formatRelative(node.last_seen_at)}` : "Heartbeat ещё не получен"}</span></Card><Card><p className="eyebrow">Health-check</p><h3>{health.filter((item) => item.state === "reachable").length} из {health.length} доступны</h3><div className="node-health-list">{health.length === 0 ? <span>Результатов пока нет</span> : health.map((item) => <Badge key={item.address} tone={item.state === "reachable" ? "success" : item.state === "unreachable" ? "danger" : "neutral"}>{item.address} · {item.state}</Badge>)}</div></Card></div>{node.last_error && <div className="validation-error" role="alert"><strong>Ошибка применения:</strong> {node.last_error}</div>}<Card className="node-routes"><div className="panel-card__header"><div><p className="eyebrow">Live IPVS</p><h2>{services.length} маршрутов</h2></div></div>{stats.length === 0 ? <div className="inline-empty"><span>Маршруты появятся после первого heartbeat со статистикой.</span></div> : <div className="traffic-table"><div className="traffic-table__head"><span>Маршрут</span><span>Соединения</span><span>Пакеты</span><span>Трафик</span></div>{stats.map((item) => <div className={`traffic-table__row ${item.backend_address ? "traffic-table__row--backend" : ""}`} key={`${item.protocol}-${item.listen_address}-${item.listen_port}-${item.backend_address}-${item.backend_port}`}><div><Badge>{item.protocol.toUpperCase()}</Badge><span className="mono">{item.backend_address ? `↳ ${item.backend_address}:${item.backend_port}` : `${item.listen_address}:${item.listen_port}`}</span></div><strong>{formatNumber(item.connections)}</strong><strong>{formatNumber(item.incoming_packets)}</strong><strong>{formatBytes(item.incoming_bytes)}</strong></div>)}</div>}</Card></div><div className="dialog__footer"><Button onClick={onClose}>Готово</Button></div></Dialog>
}

function Health({ items, nodes }: { items: BackendHealth[]; nodes: NodeInfo[] }) {
  const reachable = items.filter((item) => item.state === "reachable").length
  const [probing, setProbing] = useState(false)
  const requestProbe = async () => { setProbing(true); try { await Promise.all(nodes.filter((node) => node.status !== "disabled").map((node) => api.requestHealthProbe(node.id))); window.setTimeout(() => setProbing(false), 6000) } catch { setProbing(false) } }
  return <div className="page"><PageHeader eyebrow="ICMP monitoring" title="Health" description="Проверяется доступность хоста. Состояние TCP/UDP-приложения на порту не анализируется." action={<Button variant="secondary" disabled={probing || nodes.length === 0} onClick={requestProbe}><RefreshCw className={probing ? "spin" : ""} data-icon="inline-start" />{probing ? "Проверяю на нодах…" : "Проверить сейчас"}</Button>} />
    <div className="metrics-grid health-metrics"><Metric label="Доступны" value={String(reachable)} icon={<HeartPulse />} tone="success" /><Metric label="Недоступны" value={String(items.filter((item) => item.state === "unreachable").length)} icon={<Activity />} /><Metric label="Всего хостов" value={String(items.length)} icon={<Server />} /></div>
    <Card className="table-card">{items.length === 0 ? <EmptyState icon={<HeartPulse />} title="Ожидаю первый health-check" description="Результаты появятся после применения профиля с хотя бы одним backend." /> : <div className="health-table">{items.map((item) => <div className="health-table__row" key={`${item.node_id}-${item.address}`}><div><strong className="mono">{item.address}</strong><span>{nodes.find((node) => node.id === item.node_id)?.name ?? item.node_id}</span></div><Badge tone={item.state === "reachable" ? "success" : item.state === "unreachable" ? "danger" : "neutral"}>{item.state}</Badge><div><span>Latency</span><strong className="mono">{item.latency_millis ? `${item.latency_millis} ms` : "—"}</strong></div><div><span>Серия</span><strong className="mono">{item.state === "unreachable" ? `${item.consecutive_failures} fail` : `${item.consecutive_successes} ok`}</strong></div><time>{formatRelative(item.checked_at)}</time></div>)}</div>}</Card>
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

function ProfileDialog({ existing, health, onClose, onSaved }: { existing: { profile: Profile; revision: Revision } | null; health: BackendHealth[]; onClose: () => void; onSaved: () => Promise<void> }) {
  const initialName = existing?.profile.name ?? "Новый профиль"
  const initialDescription = existing?.profile.description ?? ""
  const initialConfig = existing?.revision.config ?? emptyConfig()
  const [name, setName] = useState(initialName)
  const [description, setDescription] = useState(initialDescription)
  const [config, setConfig] = useState<ProfileConfig>(initialConfig)
  const [revisions, setRevisions] = useState<Revision[]>([])
  const [showHistory, setShowHistory] = useState(false)
  const [busy, setBusy] = useState(false); const [error, setError] = useState("")
  useEffect(() => { if (existing) void api.revisions(existing.profile.id).then(setRevisions).catch(() => setRevisions([])) }, [existing])
  const dirty = name !== initialName || description !== initialDescription || JSON.stringify(config) !== JSON.stringify(initialConfig)
  const close = () => { if (!dirty || window.confirm("Закрыть профиль и потерять неопубликованные изменения?")) onClose() }
  const save = async () => { setBusy(true); setError(""); try { if (existing) await api.publishProfile(existing.profile.id, name, description, config); else await api.createProfile(name, description, config); await onSaved() } catch (reason) { setError(reason instanceof Error ? reason.message : "Не удалось сохранить профиль") } finally { setBusy(false) } }
  return <Dialog wide title={existing ? `Редактирование · ${existing.profile.name}` : "Новый профиль"} description={existing ? `Сохранение создаст revision ${existing.profile.current_revision + 1}` : "Настройте health-check и маршруты."} onClose={close}><div className="dialog__body">{existing && <div className="revision-toolbar"><Button variant="secondary" onClick={() => setShowHistory((value) => !value)}><History data-icon="inline-start" />История ревизий</Button><span>Текущая: rev {existing.profile.current_revision}</span></div>}{showHistory && <div className="revision-list">{revisions.map((revision) => <div key={revision.id}><div><strong>Revision {revision.number}</strong><span>{new Date(revision.created_at).toLocaleString("ru-RU")} · {revisionDiff(revision.config, initialConfig)}</span></div>{revision.number !== existing?.profile.current_revision && <Button variant="secondary" disabled={busy} onClick={async () => { if (!existing || !window.confirm(`Вернуть конфигурацию revision ${revision.number}? Будет создана новая ревизия.`)) return; setBusy(true); try { await api.rollbackProfile(existing.profile.id, revision.number); await onSaved() } catch (reason) { setError(reason instanceof Error ? reason.message : "Не удалось выполнить откат") } finally { setBusy(false) } }}>Восстановить</Button>}</div>)}</div>}<div className="profile-basics"><Field label="Название"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field><Field label="Описание"><Input value={description} onChange={(e) => setDescription(e.target.value)} /></Field></div><ProfileEditor initial={config} health={health} onChange={setConfig} />{error && <div className="validation-error" role="alert">{error}</div>}</div><div className="dialog__footer"><Button variant="ghost" onClick={close}>Отмена</Button><Button disabled={busy || !name.trim()} onClick={save}><Save data-icon="inline-start" />{busy ? "Публикую…" : "Опубликовать ревизию"}</Button></div></Dialog>
}

function PageHeader({ eyebrow, title, description, action }: { eyebrow: string; title: string; description: string; action?: React.ReactNode }) { return <header className="page-header"><div><p className="eyebrow">{eyebrow}</p><h1>{title}</h1><p>{description}</p></div>{action}</header> }
function Metric({ label, value, icon, tone }: { label: string; value: string; icon: React.ReactNode; tone?: string }) { return <Card className="metric-card"><div className={`metric-card__icon ${tone ? `metric-card__icon--${tone}` : ""}`}>{icon}</div><div><span>{label}</span><strong>{value}</strong></div></Card> }
function NodeRow({ node, profile }: { node: NodeInfo; profile?: Profile }) { const synced = node.desired_revision === node.applied_revision; return <div className="node-row"><div className="node-name"><div className="node-avatar"><Server /></div><div><strong>{node.name}</strong><span>{node.last_error || profile?.name || "Без профиля"}</span></div></div><div className="node-row__revision"><span className="mono">{node.applied_revision}/{node.desired_revision}</span><Badge tone={node.status === "error" ? "danger" : synced && node.status === "online" ? "success" : node.status === "connecting" ? "warning" : "neutral"}>{node.status === "error" ? "ошибка" : node.status === "connecting" ? "подключается" : synced && node.status === "online" ? "применено" : nodeStatusLabel(node.status)}</Badge></div></div> }
function Placeholder({ title, description, icon }: { title: string; description: string; icon: React.ReactNode }) { return <div className="page"><PageHeader eyebrow="EzhikLB" title={title} description={description} /><Card><EmptyState icon={icon} title="Раздел готовится" description={description} /></Card></div> }
function formatNumber(value: number) { return new Intl.NumberFormat("ru-RU", { notation: value >= 10000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value) }
function formatBytes(value: number) { if (value < 1024) return `${value} Б`; const units = ["КБ", "МБ", "ГБ", "ТБ"]; let next = value / 1024; let unit = 0; while (next >= 1024 && unit < units.length - 1) { next /= 1024; unit++ } return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 }).format(next)} ${units[unit]}` }
function formatRelative(value: string) { const minutes = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 60000)); if (minutes < 1) return "только что"; if (minutes < 60) return `${minutes} мин назад`; const hours = Math.floor(minutes / 60); if (hours < 24) return `${hours} ч назад`; return new Date(value).toLocaleDateString("ru-RU") }
function formatDuration(milliseconds: number) { const seconds = Math.max(0, Math.floor(milliseconds / 1000)); if (seconds < 60) return `${seconds} сек`; const minutes = Math.floor(seconds / 60); if (minutes < 60) return `${minutes} мин`; const hours = Math.floor(minutes / 60); if (hours < 24) return `${hours} ч ${minutes % 60} мин`; const days = Math.floor(hours / 24); return `${days} д ${hours % 24} ч` }
function nodeStatusLabel(status?: NodeInfo["status"]) { return ({ connecting: "подключается", online: "online", offline: "offline", error: "ошибка", disabled: "выключена" } as const)[status ?? "offline"] }
function applyStateLabel(node: NodeInfo) { if (node.status === "disabled") return "выключена"; if (node.apply_state === "applying" || node.desired_revision !== node.applied_revision) return "применяется"; if (node.apply_state === "error" || node.last_error) return "ошибка применения"; if (node.apply_state === "applied") return "конфигурация применена"; return "ожидает конфигурацию" }
function revisionDiff(value: ProfileConfig, current: ProfileConfig) { const backends = value.listeners.reduce((sum, listener) => sum + listener.backends.length, 0); if (JSON.stringify(value) === JSON.stringify(current)) return `${value.listeners.length} записей · ${backends} выходов · текущая конфигурация`; const currentBackends = current.listeners.reduce((sum, listener) => sum + listener.backends.length, 0); const listenerDelta = value.listeners.length - current.listeners.length; const backendDelta = backends - currentBackends; const changes = [`${value.listeners.length} записей`, `${backends} выходов`]; if (listenerDelta) changes.push(`${listenerDelta > 0 ? "+" : ""}${listenerDelta} записей`); if (backendDelta) changes.push(`${backendDelta > 0 ? "+" : ""}${backendDelta} выходов`); if (value.health_check.enabled !== current.health_check.enabled) changes.push(`health-check ${value.health_check.enabled ? "включён" : "выключен"}`); return changes.join(" · ") }
