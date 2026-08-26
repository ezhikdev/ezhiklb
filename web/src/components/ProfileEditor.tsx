import { ArrowRight, Copy, GripVertical, Pencil, Plus, Server, Trash2 } from "lucide-react"
import { useLayoutEffect, useMemo, useRef, useState } from "react"
import type { Backend, BackendHealth, Listener, ProfileConfig, Protocol } from "../types"
import { Badge, Button, Card, ConfirmDialog, Dialog, Field, Input, SelectMenu, Switch } from "./ui"

const makeID = (prefix: string) => {
  const bytes = new Uint8Array(8)
  if (globalThis.crypto?.getRandomValues) globalThis.crypto.getRandomValues(bytes)
  else for (let index = 0; index < bytes.length; index += 1) bytes[index] = Math.floor(Math.random() * 256)
  return `${prefix}_${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`
}

const newBackend = (): Backend => ({ id: makeID("bck"), address: "", port: 8080, weight: 1, enabled: true })
const newListener = (): Listener => ({
  id: makeID("lst"), name: "Новая запись", enabled: true, listen_address: "0.0.0.0", listen_port: 8000,
  protocols: ["udp"], scheduler: "wrr", affinity_seconds: 0, backends: [newBackend()],
})

type ListenerErrors = Record<string, string>

const affinityPresets = [
  { value: 0, label: "Выключено", description: "Новые потоки могут попадать на разные backend" },
  { value: 900, label: "15 минут", description: "Короткие UDP-сессии" },
  { value: 1800, label: "30 минут", description: "Обычные UDP-сессии" },
  { value: 3600, label: "1 час", description: "Долгие соединения" },
  { value: 10800, label: "3 часа", description: "VPN и stateful UDP" },
  { value: 18000, label: "5 часов", description: "Долгоживущие VPN-сессии" },
  { value: 86400, label: "24 часа", description: "Строго постоянный backend" },
] as const

const isIPv4 = (value: string) => {
  const parts = value.split(".")
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)
}

function validateListener(listener: Listener, others: Listener[]): ListenerErrors {
  const errors: ListenerErrors = {}
  if (!listener.name.trim()) errors.name = "Укажите название записи"
  if (!isIPv4(listener.listen_address)) errors.listen_address = "Укажите корректный IPv4-адрес"
  if (!Number.isInteger(listener.listen_port) || listener.listen_port < 1 || listener.listen_port > 65535) errors.listen_port = "Порт должен быть от 1 до 65535"
  if (listener.protocols.length === 0) errors.protocols = "Выберите TCP, UDP или оба протокола"
  if (listener.affinity_seconds < 0 || listener.affinity_seconds > 86400) errors.affinity_seconds = "Допустимо значение от 0 до 86400"
  if (listener.backends.length === 0) errors.backends = "Добавьте хотя бы один выход"
  listener.backends.forEach((backend, index) => {
    if (!isIPv4(backend.address)) errors[`backend.${index}.address`] = "Некорректный IPv4"
    if (!Number.isInteger(backend.port) || backend.port < 1 || backend.port > 65535) errors[`backend.${index}.port`] = "Порт 1–65535"
    if (!Number.isInteger(backend.weight) || backend.weight < 1 || backend.weight > 65535) errors[`backend.${index}.weight`] = "Вес 1–65535"
    if (listener.backends.some((candidate, candidateIndex) => candidateIndex < index && candidate.address === backend.address && candidate.port === backend.port)) errors[`backend.${index}.address`] = "Такой выход уже добавлен"
  })
  if (listener.enabled && !listener.backends.some((backend) => backend.enabled)) errors.backends = "У включённой записи должен быть включён хотя бы один выход"
  const conflicts = others.some((candidate) => (candidate.listen_address === listener.listen_address || candidate.listen_address === "0.0.0.0" || listener.listen_address === "0.0.0.0") && candidate.listen_port === listener.listen_port && candidate.protocols.some((protocol) => listener.protocols.includes(protocol)))
  if (conflicts) errors.listen_port = "Этот адрес, порт и протокол уже используются другой записью"
  return errors
}

export function ProfileEditor({ initial, health, nodeAddresses, onChange }: { initial: ProfileConfig; health?: BackendHealth[]; nodeAddresses?: string[]; onChange: (config: ProfileConfig) => void }) {
  const [config, setConfig] = useState<ProfileConfig>(() => structuredClone(initial))
  const [editing, setEditing] = useState<{ listener: Listener; index: number | null } | null>(null)
  const [removing, setRemoving] = useState<number | null>(null)
  const update = (next: ProfileConfig) => { setConfig(next); onChange(next) }
  const patchHealth = (values: Partial<ProfileConfig["health_check"]>) => update({ ...config, health_check: { ...config.health_check, ...values } })
  const totalBackends = useMemo(() => config.listeners.reduce((sum, listener) => sum + listener.backends.length, 0), [config.listeners])

  const saveListener = (listener: Listener) => {
    const listeners = [...config.listeners]
    if (editing?.index == null) listeners.push(listener)
    else listeners[editing.index] = listener
    update({ ...config, listeners })
    setEditing(null)
  }
  const cloneListener = (listener: Listener) => {
    const clone = structuredClone(listener)
    clone.id = makeID("lst")
    clone.name = `${listener.name} — копия`
    clone.listen_port = Math.min(65535, listener.listen_port + 1)
    clone.backends = clone.backends.map((backend) => ({ ...backend, id: makeID("bck") }))
    setEditing({ listener: clone, index: null })
  }
  const removeListener = (index: number) => { update({ ...config, listeners: config.listeners.filter((_, item) => item !== index) }); setRemoving(null) }

  return <div className="editor-stack">
    <Card className="settings-card">
      <div className="settings-card__heading">
        <div><h3>ICMP health-check</h3><p>Проверяет доступность хостов и автоматически управляет весом.</p></div>
        <Switch label="Включить health-check" checked={config.health_check.enabled} onChange={(enabled) => patchHealth({ enabled })} />
      </div>
      <div className="form-grid form-grid--four">
        <Field label="Интервал" hint="Секунды между проверками"><Input type="number" min={1} max={3600} value={config.health_check.interval_seconds} onChange={(e) => patchHealth({ interval_seconds: Number(e.target.value) })} /></Field>
        <Field label="Timeout" hint="Миллисекунды"><Input type="number" min={100} max={30000} step={100} value={config.health_check.timeout_millis} onChange={(e) => patchHealth({ timeout_millis: Number(e.target.value) })} /></Field>
        <Field label="До отключения" hint="Неудачных проверок"><Input type="number" min={1} max={100} value={config.health_check.failure_threshold} onChange={(e) => patchHealth({ failure_threshold: Number(e.target.value) })} /></Field>
        <Field label="До возврата" hint="Успешных проверок"><Input type="number" min={1} max={100} value={config.health_check.recovery_threshold} onChange={(e) => patchHealth({ recovery_threshold: Number(e.target.value) })} /></Field>
      </div>
    </Card>

    <div className="section-heading">
      <div><p className="eyebrow">Маршрутизация</p><h3>{config.listeners.length} записей · {totalBackends} выходов</h3></div>
      <Button variant="secondary" onClick={() => setEditing({ listener: newListener(), index: null })}><Plus data-icon="inline-start" />Добавить запись</Button>
    </div>

    {config.listeners.length === 0
      ? <div className="inline-empty"><Server /><div><strong>Записей пока нет</strong><span>Добавьте входной порт и хотя бы один backend.</span></div></div>
      : <RuleList listeners={config.listeners}
          onToggle={(index, enabled) => update({ ...config, listeners: config.listeners.map((item, itemIndex) => itemIndex === index ? { ...item, enabled } : item) })}
          onEdit={(index) => setEditing({ listener: structuredClone(config.listeners[index]), index })}
          onClone={(index) => cloneListener(config.listeners[index])}
          onRemove={(index) => setRemoving(index)}
          onReorder={(next) => update({ ...config, listeners: next })} />}

    {editing && <ListenerDialog initial={editing.listener} others={config.listeners.filter((_, index) => index !== editing.index)} health={health ?? []} nodeAddresses={nodeAddresses ?? []} onSave={saveListener} onClose={() => setEditing(null)} />}
    {removing != null && <ConfirmDialog title="Удалить запись?" description={`Запись «${config.listeners[removing].name}» будет удалена из профиля.`} confirmLabel="Удалить запись" danger onCancel={() => setRemoving(null)} onConfirm={() => removeListener(removing)} />}
  </div>
}

// Custom drag reordering (no library): each row's DOM node is tracked by id so a
// pointer-driven drag can move it with a direct 1:1 transform while the other rows
// FLIP-animate into their new slots. Reordering only changes array order — it never
// touches listener data, so it is purely a display/organization affordance.
function RuleList({ listeners, onToggle, onEdit, onClone, onRemove, onReorder }: { listeners: Listener[]; onToggle: (index: number, enabled: boolean) => void; onEdit: (index: number) => void; onClone: (index: number) => void; onRemove: (index: number) => void; onReorder: (next: Listener[]) => void }) {
  const rowRefs = useRef(new Map<string, HTMLDivElement>())
  const rectsRef = useRef(new Map<string, DOMRect>())
  const dragRef = useRef<{ id: string; pointerId: number; grabOffsetY: number } | null>(null)
  const lastPointerY = useRef(0)
  const [draggingId, setDraggingId] = useState<string | null>(null)

  const applyDragTransform = (id: string) => {
    const el = rowRefs.current.get(id)
    const rect = rectsRef.current.get(id)
    if (!el || !rect) return
    const info = dragRef.current
    const desiredTop = lastPointerY.current - (info?.grabOffsetY ?? 0)
    el.style.transform = `translateY(${(desiredTop - rect.top).toFixed(1)}px) scale(1.015)`
  }

  useLayoutEffect(() => {
    for (const listener of listeners) {
      const el = rowRefs.current.get(listener.id)
      if (!el) continue
      const previous = rectsRef.current.get(listener.id)
      const next = el.getBoundingClientRect()
      rectsRef.current.set(listener.id, next)
      if (listener.id === draggingId) { applyDragTransform(listener.id); continue }
      if (!previous) continue
      const dy = previous.top - next.top
      if (Math.abs(dy) < 0.5) continue
      el.style.transition = "none"
      el.style.transform = `translateY(${dy}px)`
      el.getBoundingClientRect()
      requestAnimationFrame(() => { el.style.transition = "transform .28s cubic-bezier(.22,.8,.32,1)"; el.style.transform = "" })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listeners, draggingId])

  const startDrag = (event: React.PointerEvent<HTMLButtonElement>, id: string) => {
    if (event.pointerType === "mouse" && event.button !== 0) return
    const el = rowRefs.current.get(id)
    if (!el) return
    const rect = el.getBoundingClientRect()
    dragRef.current = { id, pointerId: event.pointerId, grabOffsetY: event.clientY - rect.top }
    lastPointerY.current = event.clientY
    setDraggingId(id)
    el.style.transition = "none"
    el.style.zIndex = "5"
    event.currentTarget.setPointerCapture(event.pointerId)
    event.preventDefault()
  }

  const onHandleMove = (event: React.PointerEvent<HTMLButtonElement>) => {
    const info = dragRef.current
    if (!info || info.pointerId !== event.pointerId) return
    lastPointerY.current = event.clientY
    applyDragTransform(info.id)
    const rect = rectsRef.current.get(info.id)
    if (!rect) return
    const draggedCenter = lastPointerY.current - info.grabOffsetY + rect.height / 2
    let targetIndex = 0
    for (const listener of listeners) {
      if (listener.id === info.id) continue
      const other = rectsRef.current.get(listener.id)
      if (other && draggedCenter > other.top + other.height / 2) targetIndex++
    }
    const fromIndex = listeners.findIndex((item) => item.id === info.id)
    if (targetIndex !== fromIndex && fromIndex !== -1) {
      const next = [...listeners]
      const [moved] = next.splice(fromIndex, 1)
      next.splice(targetIndex, 0, moved)
      onReorder(next)
    }
  }

  const endDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const info = dragRef.current
    if (!info || info.pointerId !== event.pointerId) return
    const el = rowRefs.current.get(info.id)
    if (el) {
      el.style.transition = "transform .22s cubic-bezier(.22,.8,.32,1)"
      el.style.transform = ""
      el.style.zIndex = ""
    }
    dragRef.current = null
    setDraggingId(null)
  }

  return <div className="rule-list">{listeners.map((listener, index) => (
    <div key={listener.id} data-row-id={listener.id}
      ref={(el) => { if (el) rowRefs.current.set(listener.id, el); else rowRefs.current.delete(listener.id) }}
      className={`rule-row-wrap ${draggingId === listener.id ? "rule-row-wrap--dragging" : ""}`}>
      <RuleRow listener={listener}
        onToggle={(enabled) => onToggle(index, enabled)}
        onEdit={() => onEdit(index)}
        onClone={() => onClone(index)}
        onRemove={() => onRemove(index)}
        onGrabStart={(event) => startDrag(event, listener.id)}
        onGrabMove={onHandleMove}
        onGrabEnd={endDrag} />
    </div>
  ))}</div>
}

function RuleRow({ listener, onToggle, onEdit, onClone, onRemove, onGrabStart, onGrabMove, onGrabEnd }: { listener: Listener; onToggle: (enabled: boolean) => void; onEdit: () => void; onClone: () => void; onRemove: () => void; onGrabStart: (event: React.PointerEvent<HTMLButtonElement>) => void; onGrabMove: (event: React.PointerEvent<HTMLButtonElement>) => void; onGrabEnd: (event: React.PointerEvent<HTMLButtonElement>) => void }) {
  const enabledBackends = listener.backends.filter((backend) => backend.enabled)
  const totalWeight = enabledBackends.reduce((sum, backend) => sum + backend.weight, 0)
  return <Card className={`rule-row ${listener.enabled ? "" : "rule-row--disabled"}`}>
    <button type="button" className="rule-row__handle" aria-label={`Изменить порядок: ${listener.name}`} title="Перетащите, чтобы изменить порядок в списке"
      onPointerDown={onGrabStart} onPointerMove={onGrabMove} onPointerUp={onGrabEnd} onPointerCancel={onGrabEnd}><GripVertical /></button>
    <div className="rule-row__toggle"><Switch label={`${listener.enabled ? "Выключить" : "Включить"} ${listener.name}`} checked={listener.enabled} onChange={onToggle} /></div>
    <button type="button" className="rule-row__main" onClick={onEdit}>
      <div className="rule-row__name"><strong>{listener.name}</strong><span>{listener.protocols.map((item) => item.toUpperCase()).join(" + ")} · {listener.scheduler.toUpperCase()}</span></div>
      <div className="rule-route mono"><span>{listener.listen_address}:{listener.listen_port}</span><ArrowRight /><span>{enabledBackends.length} {enabledBackends.length === 1 ? "выход" : "выхода"}</span></div>
      <div className="rule-targets">{enabledBackends.slice(0, 2).map((backend) => <span key={backend.id}>{backend.address}:{backend.port} · {totalWeight ? Math.round(backend.weight / totalWeight * 100) : 0}%</span>)}{enabledBackends.length > 2 && <span>+ ещё {enabledBackends.length - 2}</span>}</div>
    </button>
    <div className="rule-row__actions">
      <Button variant="ghost" className="icon-button" aria-label={`Клонировать ${listener.name}`} title="Клонировать" onClick={onClone}><Copy /></Button>
      <Button variant="ghost" className="icon-button" aria-label={`Редактировать ${listener.name}`} title="Редактировать" onClick={onEdit}><Pencil /></Button>
      <Button variant="ghost" className="icon-button danger-hover" aria-label={`Удалить ${listener.name}`} title="Удалить" onClick={onRemove}><Trash2 /></Button>
    </div>
  </Card>
}

function ListenerDialog({ initial, others, health, nodeAddresses, onSave, onClose }: { initial: Listener; others: Listener[]; health: BackendHealth[]; nodeAddresses: string[]; onSave: (listener: Listener) => void; onClose: () => void }) {
  const [listener, setListener] = useState(initial)
  const [errors, setErrors] = useState<ListenerErrors>({})
  const [confirmClose, setConfirmClose] = useState(false)
  const dirty = JSON.stringify(listener) !== JSON.stringify(initial)
  const close = () => { if (!dirty) { onClose(); return } setConfirmClose(true) }
  const patch = (values: Partial<Listener>) => setListener((current) => ({ ...current, ...values }))
  const toggleProtocol = (protocol: Protocol) => patch({ protocols: listener.protocols.includes(protocol) ? listener.protocols.filter((item) => item !== protocol) : [...listener.protocols, protocol] })
  const patchBackend = (index: number, values: Partial<Backend>) => patch({ backends: listener.backends.map((backend, item) => item === index ? { ...backend, ...values } : backend) })
  const submit = () => {
    const nextErrors = validateListener(listener, others)
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length === 0) onSave({ ...listener, name: listener.name.trim() })
  }
  const enabledBackends = listener.backends.filter((backend) => backend.enabled)
  const totalWeight = enabledBackends.reduce((sum, backend) => sum + backend.weight, 0)

  return <Dialog wide title={initial.name === "Новая запись" ? "Новая запись" : `Редактирование · ${initial.name}`} description="Настройте входящий трафик и распределение между выходами." onClose={close}>
    <div className="dialog__body listener-dialog-body">
      <div className="listener-status-line"><Switch label="Включить запись" checked={listener.enabled} onChange={(enabled) => patch({ enabled })} /><div><strong>{listener.enabled ? "Запись включена" : "Запись выключена"}</strong><span>Изменение вступит в силу после публикации профиля.</span></div></div>
      <div className="form-grid listener-fields">
        <Field label="Название" error={errors.name}><Input value={listener.name} aria-invalid={Boolean(errors.name)} onChange={(e) => patch({ name: e.target.value })} /></Field>
        <Field label="Listen address" error={errors.listen_address}><Input className="input mono" value={listener.listen_address} aria-invalid={Boolean(errors.listen_address)} onChange={(e) => patch({ listen_address: e.target.value })} /></Field>
        <Field label="Входной порт" error={errors.listen_port}><Input type="number" min={1} max={65535} value={listener.listen_port} aria-invalid={Boolean(errors.listen_port)} onChange={(e) => patch({ listen_port: Number(e.target.value) })} /></Field>
      </div>
      <div className="listener-options">
        <Field label="Протоколы" error={errors.protocols}><div className="protocol-toggle" role="group" aria-label="Протоколы">{(["tcp", "udp"] as Protocol[]).map((protocol) => <button type="button" key={protocol} aria-pressed={listener.protocols.includes(protocol)} onClick={() => toggleProtocol(protocol)}>{protocol.toUpperCase()}</button>)}</div></Field>
        <Field label="Планировщик" hint="WRR учитывает вес, RR распределяет поровну"><SelectMenu label="Планировщик" value={listener.scheduler} onChange={(value) => patch({ scheduler: value as Listener["scheduler"] })} options={[{ value: "wrr", label: "Weighted round-robin", description: "Распределение с учётом веса" }, { value: "rr", label: "Round-robin", description: "Равномерное распределение" }]} /></Field>
      </div>

      <div className="affinity-row">
        <Field label="Affinity" hint="Закрепляет IP клиента за одним backend; для VPN обычно подходят 1–5 часов" error={errors.affinity_seconds}><SelectMenu label="Время Affinity" value={affinityPresets.some((preset) => preset.value === listener.affinity_seconds) ? String(listener.affinity_seconds) : "custom"} onChange={(value) => patch({ affinity_seconds: value === "custom" ? 300 : Number(value) })} options={[...affinityPresets.map((preset) => ({ value: String(preset.value), label: preset.label, description: preset.description })), { value: "custom", label: "Своё значение", description: "Указать время вручную в секундах" }]} /></Field>
        {affinityPresets.every((preset) => preset.value !== listener.affinity_seconds) && <Field label="Секунд" hint="1–86400"><Input type="number" min={1} max={86400} value={listener.affinity_seconds} aria-invalid={Boolean(errors.affinity_seconds)} onChange={(e) => patch({ affinity_seconds: Number(e.target.value) })} /></Field>}
      </div>

      <div className="backend-heading"><div><p className="eyebrow">Выходы</p><h3>{listener.backends.length} backend</h3></div><Button variant="secondary" onClick={() => patch({ backends: [...listener.backends, newBackend()] })}><Plus data-icon="inline-start" />Добавить выход</Button></div>
      {errors.backends && <div className="validation-error" role="alert">{errors.backends}</div>}
      <div className="backend-editor-list">{listener.backends.map((backend, index) => {
        const percent = backend.enabled && totalWeight ? Math.round(backend.weight / totalWeight * 100) : 0
        const backendHealth = health.find((item) => item.address === backend.address)
        return <Card className="backend-editor" key={backend.id}>
          <div className="backend-toggle-cell"><span>Состояние</span><Switch label={`Включить ${backend.address || "backend"}`} checked={backend.enabled} onChange={(enabled) => patchBackend(index, { enabled })} /></div>
          <Field label="IP-адрес" error={errors[`backend.${index}.address`]}><Input className="input mono" placeholder="1.1.1.1" value={backend.address} aria-invalid={Boolean(errors[`backend.${index}.address`])} onChange={(e) => patchBackend(index, { address: e.target.value })} />{!errors[`backend.${index}.address`] && backend.address && nodeAddresses.includes(backend.address) && <span className="field-warning">Совпадает с IP ноды — трафик пойдёт сам на себя</span>}</Field>
          <Field label="Порт" error={errors[`backend.${index}.port`]}><Input type="number" min={1} max={65535} value={backend.port} aria-invalid={Boolean(errors[`backend.${index}.port`])} onChange={(e) => patchBackend(index, { port: Number(e.target.value) })} /></Field>
          <Field label="Вес" hint={`${percent}% трафика`} error={errors[`backend.${index}.weight`]}><Input type="number" min={1} max={65535} value={backend.weight} aria-invalid={Boolean(errors[`backend.${index}.weight`])} onChange={(e) => patchBackend(index, { weight: Number(e.target.value) })} /></Field>
          <div className="backend-state"><Badge tone={backend.enabled ? "success" : "neutral"}>{backend.enabled ? `${percent}%` : "off"}</Badge>{backendHealth && <Badge tone={backendHealth.state === "reachable" ? "success" : backendHealth.state === "unreachable" ? "danger" : "neutral"}>{backendHealth.state === "reachable" ? `${backendHealth.latency_millis} ms` : backendHealth.state}</Badge>}</div>
          <Button variant="ghost" className="icon-button danger-hover" aria-label="Удалить backend" onClick={() => patch({ backends: listener.backends.filter((_, item) => item !== index) })}><Trash2 /></Button>
        </Card>
      })}</div>
    </div>
    <div className="dialog__footer"><Button variant="ghost" onClick={close}>Отмена</Button><Button onClick={submit}>Сохранить запись</Button></div>
    {confirmClose && <ConfirmDialog title="Закрыть без сохранения?" description="Несохранённые изменения записи будут потеряны." confirmLabel="Закрыть без сохранения" danger onCancel={() => setConfirmClose(false)} onConfirm={() => { setConfirmClose(false); onClose() }} />}
  </Dialog>
}
