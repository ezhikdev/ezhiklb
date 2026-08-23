import { Plus, Server, Trash2 } from "lucide-react"
import { useMemo, useState } from "react"
import type { Backend, Listener, ProfileConfig, Protocol } from "../types"
import { Button, Card, Field, Input, Switch } from "./ui"

const makeID = (prefix: string) => {
  const bytes = new Uint8Array(8)
  if (globalThis.crypto?.getRandomValues) {
    globalThis.crypto.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) bytes[index] = Math.floor(Math.random() * 256)
  }
  return `${prefix}_${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`
}

const newBackend = (): Backend => ({ id: makeID("bck"), address: "", port: 8080, weight: 1, enabled: true })
const newListener = (): Listener => ({
  id: makeID("lst"), name: "Новая запись", enabled: true, listen_address: "0.0.0.0", listen_port: 8000,
  protocols: ["udp"], scheduler: "wrr", affinity_seconds: 0, backends: [newBackend()],
})

export function ProfileEditor({ initial, onChange }: { initial: ProfileConfig; onChange: (config: ProfileConfig) => void }) {
  const [config, setConfig] = useState<ProfileConfig>(() => structuredClone(initial))
  const update = (next: ProfileConfig) => { setConfig(next); onChange(next) }
  const patchHealth = (values: Partial<ProfileConfig["health_check"]>) => update({ ...config, health_check: { ...config.health_check, ...values } })
  const patchListener = (index: number, values: Partial<Listener>) => {
    const listeners = [...config.listeners]
    listeners[index] = { ...listeners[index], ...values }
    update({ ...config, listeners })
  }
  const removeListener = (index: number) => update({ ...config, listeners: config.listeners.filter((_, item) => item !== index) })
  const totalBackends = useMemo(() => config.listeners.reduce((sum, listener) => sum + listener.backends.length, 0), [config.listeners])

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
      <Button variant="secondary" onClick={() => update({ ...config, listeners: [...config.listeners, newListener()] })}><Plus data-icon="inline-start" />Добавить запись</Button>
    </div>

    {config.listeners.length === 0 && <div className="inline-empty"><Server /><div><strong>Записей пока нет</strong><span>Добавьте входной порт и хотя бы один backend.</span></div></div>}
    {config.listeners.map((listener, index) => <ListenerEditor key={listener.id} listener={listener} onChange={(values) => patchListener(index, values)} onRemove={() => removeListener(index)} />)}
  </div>
}

function ListenerEditor({ listener, onChange, onRemove }: { listener: Listener; onChange: (listener: Listener) => void; onRemove: () => void }) {
  const toggleProtocol = (protocol: Protocol) => {
    const protocols = listener.protocols.includes(protocol) ? listener.protocols.filter((item) => item !== protocol) : [...listener.protocols, protocol]
    onChange({ ...listener, protocols })
  }
  const patchBackend = (index: number, values: Partial<Backend>) => {
    const backends = [...listener.backends]
    backends[index] = { ...backends[index], ...values }
    onChange({ ...listener, backends })
  }
  return <Card className="listener-card">
    <div className="listener-card__header">
      <div className="listener-card__identity"><Switch label={`Включить ${listener.name}`} checked={listener.enabled} onChange={(enabled) => onChange({ ...listener, enabled })} /><Input aria-label="Название записи" value={listener.name} onChange={(e) => onChange({ ...listener, name: e.target.value })} /></div>
      <Button variant="ghost" className="icon-button danger-hover" aria-label="Удалить запись" onClick={onRemove}><Trash2 /></Button>
    </div>
    <div className="form-grid listener-fields">
      <Field label="Listen address"><Input value={listener.listen_address} onChange={(e) => onChange({ ...listener, listen_address: e.target.value })} /></Field>
      <Field label="Входной порт"><Input type="number" min={1} max={65535} value={listener.listen_port} onChange={(e) => onChange({ ...listener, listen_port: Number(e.target.value) })} /></Field>
      <Field label="Протоколы"><div className="protocol-toggle" role="group" aria-label="Протоколы">{(["tcp", "udp"] as Protocol[]).map((protocol) => <button type="button" key={protocol} aria-pressed={listener.protocols.includes(protocol)} onClick={() => toggleProtocol(protocol)}>{protocol.toUpperCase()}</button>)}</div></Field>
      <Field label="Affinity" hint="0 — выключена"><Input type="number" min={0} max={86400} value={listener.affinity_seconds} onChange={(e) => onChange({ ...listener, affinity_seconds: Number(e.target.value) })} /></Field>
    </div>
    <div className="backend-table-wrap">
      <table className="backend-table">
        <thead><tr><th>Состояние</th><th>IP-адрес</th><th>Порт</th><th>Вес</th><th><span className="sr-only">Действия</span></th></tr></thead>
        <tbody>{listener.backends.map((backend, index) => <tr key={backend.id}>
          <td><Switch label={`Включить ${backend.address || "backend"}`} checked={backend.enabled} onChange={(enabled) => patchBackend(index, { enabled })} /></td>
          <td><Input aria-label="IP-адрес backend" placeholder="1.1.1.1" value={backend.address} onChange={(e) => patchBackend(index, { address: e.target.value })} /></td>
          <td><Input aria-label="Порт backend" type="number" min={1} max={65535} value={backend.port} onChange={(e) => patchBackend(index, { port: Number(e.target.value) })} /></td>
          <td><Input aria-label="Вес backend" type="number" min={1} max={65535} value={backend.weight} onChange={(e) => patchBackend(index, { weight: Number(e.target.value) })} /></td>
          <td><Button variant="ghost" className="icon-button danger-hover" aria-label="Удалить backend" onClick={() => onChange({ ...listener, backends: listener.backends.filter((_, item) => item !== index) })}><Trash2 /></Button></td>
        </tr>)}</tbody>
      </table>
    </div>
    <Button variant="ghost" onClick={() => onChange({ ...listener, backends: [...listener.backends, newBackend()] })}><Plus data-icon="inline-start" />Добавить выход</Button>
  </Card>
}
