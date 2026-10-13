// Janela do vídeo: recebe H.264 pelo WebSocket local, decodifica com
// WebCodecs, desenha no canvas e manda mouse e teclado de volta.

import { WindowFullscreen, WindowIsFullscreen, WindowUnfullscreen } from '../wailsjs/runtime/runtime';
import { AKEY, androidKeycode, androidMeta } from './keymap';

// Ações e botões do Android.
const DOWN = 0, UP = 1, MOVE = 2;
const BTN_PRIMARY = 1;

// Acima disso o decoder está atrasado: descarta até o próximo keyframe.
const MAX_DECODE_QUEUE = 6;
// Depois de (re)configurar, o decoder pode demorar para iniciar (no Linux o
// GStreamer monta o pipeline no primeiro frame). Nesse intervalo não descarta.
const WARMUP_MS = 1500;
// Erros seguidos do decoder (sem nenhum frame entre eles) antes de desistir,
// e a espera antes da primeira nova tentativa.
const MAX_DECODE_ERRORS = 5;
const DECODE_RETRY_MS = 200;

export interface MirrorOptions {
  url: string;
  deviceName: string;
  control: boolean;
  debug: boolean;
  onEnd: (reason: string) => void;
}

export class Mirror {
  private ws: WebSocket | null = null;
  private decoder: VideoDecoder | null = null;
  private codec = '';
  private needKey = true;
  private configuredAt = 0;
  private ended = false;

  private videoW = 0;
  private videoH = 0;
  private ctx: CanvasRenderingContext2D;

  // estatísticas
  private frames = 0;
  private framesAtLastTick = 0;
  private bytes = 0;
  private decodeMs = 0;
  private decodeCount = 0;
  private droppedLocal = 0;
  private droppedServer = 0;
  private decodeErrors = 0;
  private pending = new Map<number, number>();
  private statsTimer = 0;
  private hideTimer = 0;
  private retryTimer = 0;
  private lastStats = '';
  private ticks = 0;

  private pointerDown = false;
  private escConsumed = false;
  private listeners: Array<() => void> = [];

  constructor(
    private canvas: HTMLCanvasElement,
    private status: HTMLElement,
    private toolbar: HTMLElement,
    private statsEl: HTMLElement,
    private opt: MirrorOptions,
  ) {
    this.ctx = canvas.getContext('2d', { alpha: false, desynchronized: true })!;
  }

  start() {
    this.setStatus(`Conectando a ${this.opt.deviceName}…`);
    this.statsEl.hidden = !this.opt.debug;
    const ws = new WebSocket(this.opt.url);
    ws.binaryType = 'arraybuffer';
    ws.onmessage = (m) => (typeof m.data === 'string' ? this.onText(m.data) : this.onVideo(m.data));
    ws.onclose = () => this.end('');
    this.ws = ws;
    this.bindInput();
    this.statsTimer = window.setInterval(() => this.tickStats(), 1000);
    this.showToolbar();
  }

  stop() {
    this.ended = true;
    this.ws?.close();
    this.ws = null;
    try { this.decoder?.close(); } catch { /* já fechado */ }
    this.decoder = null;
    clearInterval(this.statsTimer);
    clearTimeout(this.hideTimer);
    clearTimeout(this.retryTimer);
    for (const off of this.listeners) off();
    this.listeners = [];
    this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
  }

  private end(reason: string) {
    if (this.ended) return;
    this.stop();
    this.opt.onEnd(reason);
  }

  private setStatus(text: string) {
    this.status.textContent = text;
    this.status.hidden = text === '';
  }

  // ---- protocolo ----

  private onText(raw: string) {
    const m = JSON.parse(raw);
    switch (m.type) {
      case 'session':
        this.videoW = m.width;
        this.videoH = m.height;
        break;
      case 'config':
        if (m.codec !== this.codec || !this.decoder || this.decoder.state === 'closed') {
          this.codec = m.codec;
          this.resetDecoder();
        }
        break;
      case 'stats':
        this.droppedServer = m.dropped;
        break;
      case 'end':
        this.end(m.reason || '');
        break;
    }
  }

  private resetDecoder() {
    try { this.decoder?.close(); } catch { /* já fechado */ }
    this.pending.clear();
    this.decoder = new VideoDecoder({
      output: (f) => this.onFrame(f),
      error: (e) => {
        this.decodeErrors++;
        console.error('decoder', e);
        if (this.decodeErrors >= MAX_DECODE_ERRORS) {
          if (this.frames === 0) this.setStatus('Não foi possível decodificar o vídeo.\n' + String(e));
          else this.end('não foi possível decodificar o vídeo (' + String(e) + ')');
          return;
        }
        // Recria o decoder e espera o próximo keyframe. A espera dobra a cada
        // erro seguido: pedir keyframe reinicia o encoder do celular.
        clearTimeout(this.retryTimer);
        this.retryTimer = window.setTimeout(() => {
          if (this.ended) return;
          this.codec = this.codec || 'avc1.42C01F';
          this.resetDecoder();
          this.requestKeyframe();
        }, DECODE_RETRY_MS * 2 ** (this.decodeErrors - 1));
      },
    });
    // Sem "description": o decoder lê SPS/PPS em Annex B dentro do keyframe.
    this.decoder.configure({ codec: this.codec, optimizeForLatency: true });
    this.configuredAt = performance.now();
    this.needKey = true;
  }

  private onVideo(buf: ArrayBuffer) {
    const b = new Uint8Array(buf);
    if (b[0] !== 1 || !this.decoder || this.decoder.state !== 'configured') return;
    const key = (b[1] & 1) === 1;
    this.bytes += b.length;
    if (this.needKey && !key) return;
    const warm = performance.now() - this.configuredAt > WARMUP_MS;
    if (!key && warm && this.decoder.decodeQueueSize > MAX_DECODE_QUEUE) {
      this.droppedLocal++;
      this.needKey = true;
      this.requestKeyframe();
      return;
    }
    this.needKey = false;
    const pts = Number(new DataView(buf).getBigInt64(2));
    this.pending.set(pts, performance.now());
    if (this.pending.size > 120) this.pending.clear();
    this.decoder.decode(new EncodedVideoChunk({ type: key ? 'key' : 'delta', timestamp: pts, data: b.subarray(10) }));
  }

  private onFrame(frame: VideoFrame) {
    const t0 = this.pending.get(frame.timestamp);
    if (t0 !== undefined) {
      this.decodeMs += performance.now() - t0;
      this.decodeCount++;
      this.pending.delete(frame.timestamp);
    }
    const w = frame.displayWidth, h = frame.displayHeight;
    if (this.canvas.width !== w || this.canvas.height !== h) {
      this.canvas.width = w;
      this.canvas.height = h;
    }
    this.videoW = w;
    this.videoH = h;
    this.ctx.drawImage(frame, 0, 0);
    frame.close();
    if (this.frames === 0) this.setStatus('');
    this.frames++;
    this.decodeErrors = 0;
  }

  private requestKeyframe() {
    this.send({ t: 'cmd', s: 'keyframe' });
  }

  private send(msg: object) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  private tickStats() {
    const fps = this.frames - this.framesAtLastTick;
    this.framesAtLastTick = this.frames;
    const kbps = Math.round((this.bytes * 8) / 1000);
    this.bytes = 0;
    const dec = this.decodeCount ? (this.decodeMs / this.decodeCount).toFixed(1) : '–';
    this.decodeMs = 0;
    this.decodeCount = 0;
    this.lastStats =
      `${this.videoW}×${this.videoH}  ${this.codec}\n` +
      `${fps} fps  ${kbps} kbps  decodificação ${dec} ms\n` +
      `fila ${this.decoder?.decodeQueueSize ?? 0}  descartados ${this.droppedLocal} local / ${this.droppedServer} servidor`;
    if (!this.statsEl.hidden) this.statsEl.textContent = this.lastStats;
    // Em --debug, as estatísticas vão para o log do Go (screensharer.log).
    if (this.opt.debug && ++this.ticks % 5 === 0) this.send({ t: 'log', s: this.lastStats.replace(/\n/g, ' | ') });
  }

  // ---- entrada ----

  private on<K extends keyof HTMLElementEventMap>(el: HTMLElement | Window, type: K, fn: (e: any) => void, opts?: AddEventListenerOptions) {
    el.addEventListener(type, fn, opts);
    this.listeners.push(() => el.removeEventListener(type, fn, opts));
  }

  // Converte coordenadas da janela para o frame de vídeo (canvas com object-fit: contain).
  private toVideo(e: PointerEvent | WheelEvent) {
    const r = this.canvas.getBoundingClientRect();
    const scale = Math.min(r.width / this.videoW, r.height / this.videoH);
    const offX = (r.width - this.videoW * scale) / 2;
    const offY = (r.height - this.videoH * scale) / 2;
    const x = Math.round((e.clientX - r.left - offX) / scale);
    const y = Math.round((e.clientY - r.top - offY) / scale);
    const inside = x >= 0 && y >= 0 && x < this.videoW && y < this.videoH;
    return {
      x: Math.max(0, Math.min(this.videoW - 1, x)),
      y: Math.max(0, Math.min(this.videoH - 1, y)),
      w: this.videoW, h: this.videoH, inside,
    };
  }

  private bindInput() {
    const c = this.canvas;
    const control = this.opt.control;
    c.focus();

    this.on(window, 'mousemove', (e: MouseEvent) => {
      if (!control || e.clientY < 80) this.showToolbar();
    });

    this.on(c, 'contextmenu', (e: Event) => e.preventDefault());

    this.on(c, 'dblclick', () => {
      if (!control) this.toggleFullscreen();
    });

    if (control) {
      this.on(c, 'pointerdown', (e: PointerEvent) => {
        if (!this.videoW) return;
        e.preventDefault();
        c.focus();
        if (e.button === 2) { // direito: voltar (ou ligar a tela)
          this.send({ t: 'back', a: DOWN });
          this.send({ t: 'back', a: UP });
          return;
        }
        if (e.button === 1) { // meio: início
          this.key(AKEY.HOME);
          return;
        }
        if (e.button !== 0) return;
        const p = this.toVideo(e);
        if (!p.inside) return;
        this.pointerDown = true;
        c.setPointerCapture(e.pointerId);
        this.send({ t: 'touch', a: DOWN, ...p, b: BTN_PRIMARY, ab: BTN_PRIMARY });
      });
      this.on(c, 'pointermove', (e: PointerEvent) => {
        if (!this.pointerDown) return;
        this.send({ t: 'touch', a: MOVE, ...this.toVideo(e), b: BTN_PRIMARY });
      });
      const up = (e: PointerEvent) => {
        if (!this.pointerDown || e.button !== 0) return;
        this.pointerDown = false;
        this.send({ t: 'touch', a: UP, ...this.toVideo(e), b: 0, ab: BTN_PRIMARY });
      };
      this.on(c, 'pointerup', up);
      this.on(c, 'pointercancel', up);
      this.on(c, 'wheel', (e: WheelEvent) => {
        e.preventDefault();
        if (!this.videoW) return;
        const unit = e.deltaMode === WheelEvent.DOM_DELTA_LINE ? 3 : e.deltaMode === WheelEvent.DOM_DELTA_PAGE ? 0.3 : 100;
        this.send({ t: 'scroll', ...this.toVideo(e), hs: -e.deltaX / unit, vs: -e.deltaY / unit });
      }, { passive: false });
    }

    this.on(window, 'keydown', (e: KeyboardEvent) => this.onKey(e, DOWN));
    this.on(window, 'keyup', (e: KeyboardEvent) => this.onKey(e, UP));

    for (const btn of Array.from(this.toolbar.querySelectorAll<HTMLButtonElement>('button[data-key]'))) {
      this.on(btn, 'click', () => {
        const code = Number(btn.dataset.key);
        if (code === AKEY.BACK) {
          this.send({ t: 'back', a: DOWN });
          this.send({ t: 'back', a: UP });
        } else {
          this.key(code);
        }
        c.focus();
      });
    }
    for (const btn of Array.from(this.toolbar.querySelectorAll<HTMLButtonElement>('button[data-cmd]'))) {
      this.on(btn, 'click', () => { this.send({ t: 'cmd', s: btn.dataset.cmd }); c.focus(); });
    }
    this.on(document.getElementById('fs-btn')!, 'click', () => this.toggleFullscreen());
    this.on(document.getElementById('stats-btn')!, 'click', () => this.toggleStats());
    this.on(document.getElementById('stop-btn')!, 'click', () => this.end(''));
  }

  private key(code: number) {
    this.send({ t: 'key', a: DOWN, code });
    this.send({ t: 'key', a: UP, code });
  }

  private async onKey(e: KeyboardEvent, action: number) {
    // Atalhos do próprio app (não vão para o celular).
    if (e.code === 'F11') {
      e.preventDefault();
      if (action === DOWN) this.toggleFullscreen();
      return;
    }
    if (e.ctrlKey && e.shiftKey && e.code === 'KeyD') {
      e.preventDefault();
      if (action === DOWN) this.toggleStats();
      return;
    }
    if (e.code === 'Escape') {
      if (action === UP && this.escConsumed) {
        this.escConsumed = false; // o Esc que saiu da tela cheia não vai para o celular
        return;
      }
      if (action === DOWN && (await WindowIsFullscreen())) {
        this.escConsumed = true;
        WindowUnfullscreen();
        return;
      }
    }
    if (!this.opt.control) return;
    e.preventDefault();

    if (e.ctrlKey && !e.altKey && e.code === 'KeyV') {
      if (action === DOWN) this.send({ t: 'paste' }); // cola o clipboard do PC no celular
      return;
    }
    if (e.ctrlKey && !e.altKey && e.code === 'KeyC') {
      if (action === DOWN) this.send({ t: 'copy' }); // copia a seleção do celular para o PC
      return;
    }
    // Texto comum vai como texto, para respeitar o layout do teclado (ç, acentos…).
    // O AltGr chega como Ctrl+Alt no Windows, mas produz um caractere (/, ?, @…).
    const altGr = e.getModifierState('AltGraph');
    if ((altGr || (!e.ctrlKey && !e.altKey)) && !e.metaKey && e.key.length === 1) {
      if (action === DOWN) this.send({ t: 'text', s: e.key });
      return;
    }
    const code = androidKeycode(e.code);
    if (code === undefined) return;
    this.send({ t: 'key', a: action, code, meta: androidMeta(e), repeat: e.repeat ? 1 : 0 });
  }

  private async toggleFullscreen() {
    if (await WindowIsFullscreen()) WindowUnfullscreen();
    else WindowFullscreen();
    this.canvas.focus();
  }

  private toggleStats() {
    this.statsEl.hidden = !this.statsEl.hidden;
    this.statsEl.textContent = this.lastStats;
  }

  private showToolbar() {
    this.toolbar.classList.remove('hidden');
    clearTimeout(this.hideTimer);
    this.hideTimer = window.setTimeout(() => this.toolbar.classList.add('hidden'), 2000);
  }
}
