import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"

type Axis = "vertical" | "grid"

interface DragState {
  id: string
  pointerId: number
  startX: number
  startY: number
  snapshot: string[]
}

export function useDragReorder<T>({
  items,
  getID,
  axis,
  onReorder,
  onCommit,
  onError,
}: {
  items: T[]
  getID: (item: T) => string
  axis: Axis
  onReorder: (items: T[]) => void
  onCommit?: (items: T[]) => Promise<void> | void
  onError?: (error: unknown) => void
}) {
  const elements = useRef(new Map<string, HTMLElement>())
  const overlay = useRef<HTMLElement | null>(null)
  const drag = useRef<DragState | null>(null)
  const pendingRects = useRef<Map<string, DOMRect> | null>(null)
  const itemsRef = useRef(items)
  const callbacks = useRef({ getID, onReorder, onCommit, onError })
  const [activeID, setActiveID] = useState<string | null>(null)
  itemsRef.current = items
  callbacks.current = { getID, onReorder, onCommit, onError }

  const rememberPaintedPositions = () => {
    const positions = new Map<string, DOMRect>()
    for (const [id, element] of elements.current) positions.set(id, element.getBoundingClientRect())
    pendingRects.current = positions
  }

  useLayoutEffect(() => {
    const previous = pendingRects.current
    pendingRects.current = null
    if (!previous || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
    for (const item of items) {
      const id = getID(item)
      if (id === activeID) continue
      const element = elements.current.get(id)
      const from = previous.get(id)
      if (!element || !from) continue
      element.style.transition = "none"
      element.style.transform = ""
      const to = element.getBoundingClientRect()
      const dx = from.left - to.left
      const dy = from.top - to.top
      if (Math.abs(dx) < .5 && Math.abs(dy) < .5) continue
      element.style.transform = `translate3d(${dx}px, ${dy}px, 0)`
      element.getBoundingClientRect()
      requestAnimationFrame(() => {
        element.style.transition = "transform .22s cubic-bezier(.22,.8,.32,1)"
        element.style.transform = ""
      })
    }
  }, [activeID, getID, items])

  const removeOverlay = useCallback(() => {
    overlay.current?.remove()
    overlay.current = null
  }, [])

  const finish = useCallback((cancelled: boolean) => {
    const current = drag.current
    if (!current) return
    drag.current = null
    const currentItems = itemsRef.current
    const snapshotItems = current.snapshot.map((id) => currentItems.find((item) => callbacks.current.getID(item) === id)).filter((item): item is T => Boolean(item))
    if (cancelled && snapshotItems.length === currentItems.length) callbacks.current.onReorder(snapshotItems)
    const target = elements.current.get(current.id)
    const clone = overlay.current
    const settle = () => { removeOverlay(); setActiveID(null) }
    if (clone && target && !cancelled && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
      const from = clone.getBoundingClientRect()
      const to = target.getBoundingClientRect()
      clone.style.transform = "none"
      clone.style.left = `${from.left}px`
      clone.style.top = `${from.top}px`
      clone.style.width = `${from.width}px`
      clone.getBoundingClientRect()
      clone.classList.add("drag-overlay--dropping")
      clone.style.left = `${to.left}px`
      clone.style.top = `${to.top}px`
      clone.style.width = `${to.width}px`
      window.setTimeout(settle, 190)
    } else {
      settle()
    }
    if (!cancelled && callbacks.current.onCommit) {
      Promise.resolve(callbacks.current.onCommit(currentItems)).catch((error) => {
        if (snapshotItems.length === currentItems.length) callbacks.current.onReorder(snapshotItems)
        callbacks.current.onError?.(error)
      })
    }
  }, [removeOverlay])

  useEffect(() => {
    const move = (event: PointerEvent) => {
      const current = drag.current
      if (!current || event.pointerId !== current.pointerId || !overlay.current) return
      event.preventDefault()
      overlay.current.style.transform = `translate3d(${event.clientX - current.startX}px, ${event.clientY - current.startY}px, 0)`

      const edge = 56
      if (event.clientY < edge) window.scrollBy({ top: -18 })
      else if (event.clientY > window.innerHeight - edge) window.scrollBy({ top: 18 })
      const scrollParent = nearestScrollParent(elements.current.get(current.id) ?? null)
      if (scrollParent) {
        const bounds = scrollParent.getBoundingClientRect()
        if (event.clientY < bounds.top + edge) scrollParent.scrollTop -= 14
        else if (event.clientY > bounds.bottom - edge) scrollParent.scrollTop += 14
      }

      const currentItems = itemsRef.current
      const fromIndex = currentItems.findIndex((item) => callbacks.current.getID(item) === current.id)
      if (fromIndex < 0) return
      let targetIndex = fromIndex
      if (axis === "vertical") {
        targetIndex = 0
        for (const item of currentItems) {
          const id = callbacks.current.getID(item)
          if (id === current.id) continue
          const rect = elements.current.get(id)?.getBoundingClientRect()
          if (rect && event.clientY > rect.top + rect.height / 2) targetIndex += 1
        }
      } else {
        let distance = Number.POSITIVE_INFINITY
        currentItems.forEach((item, index) => {
          const id = callbacks.current.getID(item)
          if (id === current.id) return
          const rect = elements.current.get(id)?.getBoundingClientRect()
          if (!rect) return
          const dx = event.clientX - (rect.left + rect.width / 2)
          const dy = event.clientY - (rect.top + rect.height / 2)
          const nextDistance = dx * dx + dy * dy
          if (nextDistance < distance) { distance = nextDistance; targetIndex = index }
        })
      }
      if (targetIndex === fromIndex) return
      rememberPaintedPositions()
      const next = [...currentItems]
      const [moved] = next.splice(fromIndex, 1)
      next.splice(targetIndex, 0, moved)
      itemsRef.current = next
      callbacks.current.onReorder(next)
    }
    const up = (event: PointerEvent) => {
      if (drag.current && event.pointerId === drag.current.pointerId) finish(false)
    }
    const cancel = (event: PointerEvent) => {
      if (drag.current && event.pointerId === drag.current.pointerId) finish(true)
    }
    window.addEventListener("pointermove", move, { passive: false })
    window.addEventListener("pointerup", up)
    window.addEventListener("pointercancel", cancel)
    return () => {
      window.removeEventListener("pointermove", move)
      window.removeEventListener("pointerup", up)
      window.removeEventListener("pointercancel", cancel)
      removeOverlay()
    }
  }, [axis, finish, removeOverlay])

  const start = (event: React.PointerEvent<HTMLElement>, id: string) => {
    if ((event.pointerType === "mouse" && event.button !== 0) || drag.current) return
    const source = elements.current.get(id)
    if (!source) return
    const rect = source.getBoundingClientRect()
    const clone = source.cloneNode(true) as HTMLElement
    clone.classList.add("drag-overlay")
    clone.setAttribute("aria-hidden", "true")
    Object.assign(clone.style, { left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px`, transition: "none", transform: "translate3d(0, 0, 0)" })
    document.body.appendChild(clone)
    overlay.current = clone
    drag.current = { id, pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, snapshot: items.map(getID) }
    setActiveID(id)
    event.currentTarget.setPointerCapture?.(event.pointerId)
    event.preventDefault()
  }

  const moveWithKeyboard = (event: React.KeyboardEvent<HTMLElement>, id: string) => {
    const delta = event.key === "ArrowUp" || event.key === "ArrowLeft" ? -1 : event.key === "ArrowDown" || event.key === "ArrowRight" ? 1 : 0
    if (!delta) return
    const index = items.findIndex((item) => getID(item) === id)
    const target = Math.max(0, Math.min(items.length - 1, index + delta))
    if (index < 0 || index === target) return
    event.preventDefault()
    rememberPaintedPositions()
    const next = [...items]
    const [moved] = next.splice(index, 1)
    next.splice(target, 0, moved)
    itemsRef.current = next
    onReorder(next)
    Promise.resolve(onCommit?.(next)).catch((error) => { onReorder(items); onError?.(error) })
  }

  return {
    activeID,
    itemRef: (id: string) => (element: HTMLElement | null) => { if (element) elements.current.set(id, element); else elements.current.delete(id) },
    handleProps: (id: string) => ({
      onPointerDown: (event: React.PointerEvent<HTMLElement>) => start(event, id),
      onKeyDown: (event: React.KeyboardEvent<HTMLElement>) => moveWithKeyboard(event, id),
      "aria-keyshortcuts": "ArrowUp ArrowDown ArrowLeft ArrowRight",
    }),
  }
}

function nearestScrollParent(element: HTMLElement | null): HTMLElement | null {
  let parent = element?.parentElement ?? null
  while (parent && parent !== document.body) {
    const style = getComputedStyle(parent)
    if (/(auto|scroll)/.test(style.overflowY)) return parent
    parent = parent.parentElement
  }
  return null
}
