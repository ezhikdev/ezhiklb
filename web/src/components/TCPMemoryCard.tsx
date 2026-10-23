import { CircleAlert, Save } from "lucide-react"
import { useEffect, useState } from "react"
import { api } from "../lib/api"
import type { NodeInfo } from "../types"
import { Badge, Button, Card, Field, Input, Switch } from "./ui"

/** First agent that manages tcp_mem (domain.TCPTuningMinAgentVersion). */
export const tcpTuningMinAgentVersion = "2.0.4"
const minPercent = 10
const maxPercent = 90
const defaultPercent = 70

const formatSize = (bytes: number) => bytes >= 1024 ** 3
  ? `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 }).format(bytes / 1024 ** 3)} ГБ`
  : `${Math.round(bytes / 1024 ** 2)} МБ`

/** True when the node reports kernel TCP memory pressure in its last check. */
export const tcpMemoryPressure = (node: NodeInfo) => (node.diagnostics?.tcp_memory?.pressure_events ?? 0) > 0

/** Kernel TCP buffer memory of a node: usage, limits, automation and its log. */
export function TCPMemoryCard({ node, supported, onChanged }: { node: NodeInfo; supported: boolean; onChanged: () => Promise<void> }) {
  const status = node.diagnostics?.tcp_memory
  const [enabled, setEnabled] = useState(node.tcp_tuning_enabled)
  const [percent, setPercent] = useState(String(node.tcp_memory_percent || defaultPercent))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  useEffect(() => { setEnabled(node.tcp_tuning_enabled); setPercent(String(node.tcp_memory_percent || defaultPercent)) }, [node.tcp_tuning_enabled, node.tcp_memory_percent])
  const value = Number(percent)
  const valid = Number.isInteger(value) && value >= minPercent && value <= maxPercent
  const dirty = enabled !== node.tcp_tuning_enabled || value !== node.tcp_memory_percent
  const save = async () => {
    setBusy(true)
    setError("")
    try { await api.updateNodeTCPTuning(node.id, enabled, value); await onChanged() }
    catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
    finally { setBusy(false) }
  }
  const limits = status?.limit_bytes ?? [0, 0, 0]
  const used = status?.used_bytes ?? 0
  const maximum = limits[2]
  const pressure = tcpMemoryPressure(node)
  const events = [...(status?.events ?? [])].reverse()

  return <Card className="tcp-memory-card">
    <div className="panel-card__header">
      <div>
        <p className="eyebrow">Память TCP</p>
        <h2>{maximum ? `${formatSize(used)} из ${formatSize(maximum)}` : "—"}</h2>
        <span>{status?.ram_bytes
          ? `RAM ${formatSize(status.ram_bytes)} · ${status.cpus} vCPU · безопасный предел ${formatSize(status.ceiling_bytes)}`
          : supported ? "Данные появятся после следующей проверки агента (раз в минуту)." : `Требуется агент ${tcpTuningMinAgentVersion} или новее.`}</span>
      </div>
      <div className="tcp-memory-card__toggle">
        <Switch label="Автоматика памяти TCP" checked={enabled} disabled={busy || !supported} onChange={setEnabled} />
        <div><strong>{enabled ? "Автоматика" : "Не управляется"}</strong><span>{enabled ? "агент подбирает лимит" : "лимиты не меняются"}</span></div>
      </div>
    </div>
    <div className="tcp-memory-card__body">
      {status?.warning && <div className={`tcp-memory-warning ${pressure ? "tcp-memory-warning--danger" : ""}`} role="alert"><CircleAlert /><span>{status.warning}</span></div>}
      {maximum > 0 && <>
        <div className="tcp-memory-meter" role="meter" aria-label="Использование памяти TCP" aria-valuemin={0} aria-valuemax={maximum} aria-valuenow={used}>
          <i className={pressure ? "tcp-memory-meter__fill--danger" : ""} style={{ width: `${Math.min(100, used * 100 / maximum)}%` }} />
          <b style={{ left: `${limits[1] * 100 / maximum}%` }} title={`Давление с ${formatSize(limits[1])}`} />
        </div>
        <div className="tcp-memory-limits">
          <span>Порог <strong className="mono">{formatSize(limits[0])}</strong></span>
          <span>Давление <strong className="mono">{formatSize(limits[1])}</strong></span>
          <span>Максимум <strong className="mono">{formatSize(maximum)}</strong></span>
          {(status?.adaptive_percent ?? 0) > 0 && <Badge tone="warning">+{status!.adaptive_percent}% RAM адаптивно</Badge>}
        </div>
      </>}
      <div className="tcp-memory-settings">
        <Field label="Максимум, % RAM" hint={`По умолчанию ${defaultPercent}%. Не меньше 15% RAM (минимум 512 МБ) всегда остаётся системе и HAProxy.`} error={!valid ? `От ${minPercent} до ${maxPercent}` : error}>
          <Input type="number" min={minPercent} max={maxPercent} value={percent} disabled={!enabled || busy || !supported} aria-invalid={!valid} onChange={(event) => setPercent(event.target.value)} />
        </Field>
        <Button disabled={!dirty || !valid || busy || !supported} onClick={() => { void save() }}><Save data-icon="inline-start" />{busy ? "Сохраняю…" : "Сохранить"}</Button>
      </div>
      <p className="tcp-memory-note">Агент проверяет RAM, vCPU и TCP раз в минуту — изменения тарифа VPS видны без перезагрузки. При нехватке памяти TCP лимит растёт шагами по 5% RAM, пока свободной памяти хватает и нет признаков OOM. Снижается он только когда соединения не используют эту память.</p>
      {events.length > 0 && <div className="tcp-memory-events">
        <p className="eyebrow">Журнал</p>
        {events.map((event, index) => <div key={`${event.at}-${index}`} className={`tcp-memory-event tcp-memory-event--${event.level}`}><time className="mono">{new Date(event.at).toLocaleString("ru-RU")}</time><span>{event.message}</span></div>)}
      </div>}
    </div>
  </Card>
}
