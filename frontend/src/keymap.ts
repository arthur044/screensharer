// Mapeia KeyboardEvent.code para keycodes do Android (android/keycodes.h).
// Letras, números e símbolos sem Ctrl/Alt vão como texto (INJECT_TEXT), que
// respeita o layout do teclado do PC; este mapa cobre as teclas especiais e
// os atalhos com modificador.

export const AKEY = {
  HOME: 3,
  BACK: 4,
  APP_SWITCH: 187,
  POWER: 26,
  VOLUME_UP: 24,
  VOLUME_DOWN: 25,
  MENU: 82,
} as const;

const special: Record<string, number> = {
  Enter: 66,
  NumpadEnter: 160,
  Backspace: 67,
  Delete: 112,
  Tab: 61,
  Escape: 111,
  Space: 62,
  ArrowUp: 19,
  ArrowDown: 20,
  ArrowLeft: 21,
  ArrowRight: 22,
  PageUp: 92,
  PageDown: 93,
  Home: 122,
  End: 123,
  Insert: 124,
  ShiftLeft: 59,
  ShiftRight: 60,
  ControlLeft: 113,
  ControlRight: 114,
  AltLeft: 57,
  AltRight: 58,
  MetaLeft: 117,
  CapsLock: 115,
  Comma: 55,
  Period: 56,
  Minus: 69,
  Equal: 70,
  BracketLeft: 71,
  BracketRight: 72,
  Backslash: 73,
  Semicolon: 74,
  Quote: 75,
  Slash: 76,
  Backquote: 68,
  ContextMenu: 82,
  AudioVolumeUp: 24,
  AudioVolumeDown: 25,
};

export function androidKeycode(code: string): number | undefined {
  if (code in special) return special[code];
  let m = /^Key([A-Z])$/.exec(code);
  if (m) return 29 + (m[1].charCodeAt(0) - 65); // A=29 … Z=54
  m = /^Digit([0-9])$/.exec(code);
  if (m) return 7 + Number(m[1]); // 0=7 … 9=16
  m = /^Numpad([0-9])$/.exec(code);
  if (m) return 144 + Number(m[1]);
  m = /^F([0-9]{1,2})$/.exec(code);
  if (m && +m[1] >= 1 && +m[1] <= 12) return 130 + Number(m[1]); // F1=131
  return undefined;
}

// Metastate do Android (android/input.h).
export function androidMeta(e: KeyboardEvent): number {
  let meta = 0;
  if (e.shiftKey) meta |= 0x01 | 0x40;
  if (e.altKey) meta |= 0x02 | 0x10;
  if (e.ctrlKey) meta |= 0x1000 | 0x2000;
  if (e.metaKey) meta |= 0x10000;
  if (e.getModifierState?.('CapsLock')) meta |= 0x100000;
  return meta;
}
