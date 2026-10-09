import { create } from 'zustand'

export type Toast = { id: number; tone: 'info' | 'success' | 'error'; text: string }

type ToastState = { toasts: Toast[] }

export const useToasts = create<ToastState>(() => ({ toasts: [] }))

let nextId = 1

export function toast(text: string, tone: Toast['tone'] = 'info', ms = 5000) {
  const id = nextId++
  useToasts.setState((s) => ({ toasts: [...s.toasts.slice(-3), { id, tone, text }] }))
  setTimeout(() => dismissToast(id), ms)
}

export function dismissToast(id: number) {
  useToasts.setState((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }))
}
