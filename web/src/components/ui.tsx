import { Activity, Check, ChevronDown, X } from "lucide-react"
import type { ButtonHTMLAttributes, HTMLAttributes, InputHTMLAttributes, ReactNode } from "react"
import { useEffect, useId, useRef, useState } from "react"
import { createPortal } from "react-dom"

export function Button({ className = "", variant = "primary", type = "button", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "ghost" | "danger" }) {
  return <button type={type} className={`button button--${variant} ${className}`} {...props} />
}

export function Card({ className = "", ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={`card ${className}`} {...props} />
}

export function Badge({ tone = "neutral", children }: { tone?: "neutral" | "success" | "warning" | "danger"; children: ReactNode }) {
  return <span className={`badge badge--${tone}`}><span className="badge__dot" aria-hidden="true" />{children}</span>
}

export function Field({ label, hint, error, children }: { label: string; hint?: string; error?: string; children: ReactNode }) {
  return <label className={`field ${error ? "field--invalid" : ""}`}>
    <span className="field__label">{label}</span>
    {children}
    {error ? <span className="field__error">{error}</span> : hint ? <span className="field__hint">{hint}</span> : null}
  </label>
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input className="input" {...props} />
}

export function Switch({ checked, onChange, label }: { checked: boolean; onChange: (checked: boolean) => void; label: string }) {
  return <button type="button" role="checkbox" aria-checked={checked} aria-label={label} className={`switch ${checked ? "switch--on" : ""}`} onClick={() => onChange(!checked)}><span aria-hidden="true"><Check /></span></button>
}

export function SelectMenu({ value, options, onChange, label, compact = false }: { value: string; options: { value: string; label: string; description?: string }[]; onChange: (value: string) => void; label: string; compact?: boolean }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const selected = options.find((option) => option.value === value) ?? options[0]
  useEffect(() => {
    const close = (event: MouseEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    window.addEventListener("mousedown", close)
    return () => window.removeEventListener("mousedown", close)
  }, [])
  return <div ref={root} className={`select-menu ${compact ? "select-menu--compact" : ""}`}>
    <Button variant="secondary" className="select-menu__trigger" aria-haspopup="listbox" aria-expanded={open} aria-label={label} onClick={() => setOpen((current) => !current)}><span><strong>{selected?.label ?? "Выберите"}</strong>{selected?.description && <small>{selected.description}</small>}</span><ChevronDown className={open ? "select-menu__chevron select-menu__chevron--open" : "select-menu__chevron"} /></Button>
    {open && <div className="select-menu__popover" role="listbox" aria-label={label}>{options.map((option) => <button type="button" role="option" aria-selected={option.value === value} key={option.value} onClick={() => { onChange(option.value); setOpen(false) }}><span><strong>{option.label}</strong>{option.description && <small>{option.description}</small>}</span>{option.value === value && <Check />}</button>)}</div>}
  </div>
}

export function Dialog({ title, description, children, onClose, wide = false }: { title: string; description?: string; children: ReactNode; onClose: () => void; wide?: boolean }) {
  const ref = useRef<HTMLDivElement>(null)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose
  const titleID = useId()
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = "hidden"
    ref.current?.focus()
    const key = (event: KeyboardEvent) => {
      const dialogs = [...document.querySelectorAll<HTMLElement>('[role="dialog"]')]
      if (dialogs.at(-1) !== ref.current) return
      if (event.key === "Escape") onCloseRef.current()
      if (event.key === "Tab" && ref.current) {
        const items = [...ref.current.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])')]
        if (!items.length) return
        const first = items[0], last = items[items.length - 1]
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
      }
    }
    window.addEventListener("keydown", key)
    return () => { window.removeEventListener("keydown", key); document.body.style.overflow = previousOverflow; previous?.focus() }
  }, [])
  return createPortal(<div className="dialog-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
    <div ref={ref} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby={titleID} className={`dialog ${wide ? "dialog--wide" : ""}`}>
      <div className="dialog__header">
        <div><h2 id={titleID}>{title}</h2>{description && <p>{description}</p>}</div>
        <Button variant="ghost" className="icon-button" onClick={onClose} aria-label="Закрыть"><X /></Button>
      </div>
      {children}
    </div>
  </div>, document.body)
}

export function EmptyState({ icon, title, description, action }: { icon: ReactNode; title: string; description: string; action?: ReactNode }) {
  return <div className="empty-state"><div className="empty-state__icon">{icon}</div><h3>{title}</h3><p>{description}</p>{action}</div>
}

export function ConfirmDialog({ title, description, confirmLabel, danger = false, busy = false, onCancel, onConfirm }: { title: string; description: string; confirmLabel: string; danger?: boolean; busy?: boolean; onCancel: () => void; onConfirm: () => void | Promise<void> }) {
  return <Dialog title={title} description="Подтвердите действие" onClose={onCancel}><div className="dialog__body confirm-content"><Activity /><p>{description}</p></div><div className="dialog__footer"><Button variant="ghost" disabled={busy} onClick={onCancel}>Отмена</Button><Button variant={danger ? "danger" : "primary"} disabled={busy} onClick={() => { void onConfirm() }}>{busy ? "Выполняю…" : confirmLabel}</Button></div></Dialog>
}
