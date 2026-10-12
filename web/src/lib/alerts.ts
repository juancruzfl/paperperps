// Alerts for things that happen while you're not clicking: resting orders filling, stops
// and take-profits firing, liquidations, price alerts (priceAlerts.ts). Each shows a toast, plays a short sound, and, when
// the tab is in the background and you've allowed it, a desktop notification.
// Preferences are per browser, in localStorage.

import { create } from 'zustand'
import { toast } from './toasts'

const SOUND_KEY = 'paperperps.sounds'
const DESKTOP_KEY = 'paperperps.desktopAlerts'

function read(key: string, fallback: boolean): boolean {
  try {
    const v = localStorage.getItem(key)
    return v === null ? fallback : v === 'on'
  } catch {
    return fallback
  }
}

function write(key: string, on: boolean) {
  try {
    localStorage.setItem(key, on ? 'on' : 'off')
  } catch {
    // Private mode: the choice lasts for this tab.
  }
}

type AlertPrefs = { sounds: boolean; desktop: boolean }

export const useAlertPrefs = create<AlertPrefs>(() => ({
  sounds: read(SOUND_KEY, true),
  desktop: read(DESKTOP_KEY, false) && desktopPermission() === 'granted',
}))

export function desktopPermission(): NotificationPermission | 'unsupported' {
  return typeof Notification === 'undefined' ? 'unsupported' : Notification.permission
}

export function setSounds(on: boolean) {
  write(SOUND_KEY, on)
  useAlertPrefs.setState({ sounds: on })
  if (on) playTone('fill') // let people hear what they turned on
}

/** Turns desktop notifications on, asking the browser for permission if needed. */
export async function setDesktop(on: boolean) {
  if (!on) {
    write(DESKTOP_KEY, false)
    useAlertPrefs.setState({ desktop: false })
    return
  }
  if (desktopPermission() === 'unsupported') {
    toast('This browser doesn’t support notifications', 'error')
    return
  }
  const permission = Notification.permission === 'default' ? await Notification.requestPermission() : Notification.permission
  if (permission !== 'granted') {
    toast('Notifications are blocked for this site. Allow them in your browser’s site settings.', 'error', 8000)
    return
  }
  write(DESKTOP_KEY, true)
  useAlertPrefs.setState({ desktop: true })
}

let audio: AudioContext | null = null

/** A short two-note chime: rising for a fill, falling and lower for a liquidation. */
export function playTone(kind: 'fill' | 'liquidation') {
  if (!useAlertPrefs.getState().sounds || typeof AudioContext === 'undefined') return
  try {
    audio ??= new AudioContext()
    void audio.resume() // browsers start audio suspended until the page has been used
    const notes = kind === 'fill' ? [880, 1320] : [440, 247]
    const t0 = audio.currentTime
    notes.forEach((freq, i) => {
      const osc = audio!.createOscillator()
      const gain = audio!.createGain()
      const start = t0 + i * 0.11
      osc.type = kind === 'fill' ? 'sine' : 'triangle'
      osc.frequency.value = freq
      gain.gain.setValueAtTime(0.0001, start)
      gain.gain.exponentialRampToValueAtTime(0.18, start + 0.015)
      gain.gain.exponentialRampToValueAtTime(0.0001, start + 0.22)
      osc.connect(gain).connect(audio!.destination)
      osc.start(start)
      osc.stop(start + 0.25)
    })
  } catch {
    // No audio device, or the browser refused: alerts still show.
  }
}

// One order can fill in several pieces; alert once.
const recent = new Map<string, number>()
const DEDUPE_MS = 3000

export function alertOnce(key: string): boolean {
  const now = Date.now()
  for (const [k, at] of recent) if (now - at > DEDUPE_MS) recent.delete(k)
  if (recent.has(key)) return false
  recent.set(key, now)
  return true
}

/** Toast, sound, and (in the background, if enabled) a desktop notification. */
export function raiseAlert(kind: 'fill' | 'liquidation' | 'price', title: string, body: string) {
  toast(body, kind === 'liquidation' ? 'error' : kind === 'price' ? 'info' : 'success', kind === 'liquidation' ? 15000 : 8000)
  playTone(kind === 'liquidation' ? 'liquidation' : 'fill')
  if (useAlertPrefs.getState().desktop && desktopPermission() === 'granted' && document.hidden) {
    try {
      new Notification(title, { body, icon: '/icon-192.png', tag: `paperperps-${kind}` })
    } catch {
      // Some mobile browsers only allow notifications from a service worker.
    }
  }
}
