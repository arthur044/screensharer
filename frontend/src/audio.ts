// Som do celular: recebe PCM (16 bits, 48 kHz, estéreo) pelo mesmo WebSocket
// do vídeo e toca com Web Audio. Um AudioWorklet guarda uma fila curta, para
// absorver a irregularidade da chegada sem acumular atraso.

const SAMPLE_RATE = 48000;
const CHANNELS = 2;

// Fila do worklet, em milissegundos: começa a tocar ao juntar TARGET_MS e, se
// passar de MAX_MS (o PC ficou para trás), joga fora o excesso.
const TARGET_MS = 60;
const MAX_MS = 200;

// Roda na thread de áudio. Recebe blocos Float32 intercalados.
const WORKLET = `
class PcmPlayer extends AudioWorkletProcessor {
  constructor(options) {
    super();
    this.target = options.processorOptions.target;
    this.max = options.processorOptions.max;
    this.chunks = [];
    this.offset = 0;   // posição (em frames) dentro do primeiro bloco
    this.frames = 0;   // frames na fila
    this.playing = false;
    this.underruns = 0;
    this.dropped = 0;
    this.ticks = 0;
    this.port.onmessage = (e) => {
      this.chunks.push(e.data);
      this.frames += e.data.length / 2;
      if (this.frames > this.max) this.skip(this.frames - this.target);
    };
  }
  skip(n) {
    this.dropped += n;
    this.frames -= n;
    while (n > 0) {
      const left = this.chunks[0].length / 2 - this.offset;
      if (n >= left) { this.chunks.shift(); this.offset = 0; n -= left; }
      else { this.offset += n; n = 0; }
    }
  }
  process(inputs, outputs) {
    const left = outputs[0][0], right = outputs[0][1] || left;
    if (!this.playing && this.frames >= this.target) this.playing = true;
    if (this.playing) {
      let i = 0;
      while (i < left.length && this.chunks.length) {
        const c = this.chunks[0];
        const n = Math.min(left.length - i, c.length / 2 - this.offset);
        for (let k = 0; k < n; k++) {
          left[i + k] = c[(this.offset + k) * 2];
          right[i + k] = c[(this.offset + k) * 2 + 1];
        }
        i += n;
        this.offset += n;
        this.frames -= n;
        if (this.offset * 2 >= c.length) { this.chunks.shift(); this.offset = 0; }
      }
      if (i < left.length) { // fila vazia: silêncio até juntar de novo
        this.playing = false;
        this.underruns++;
      }
    }
    if (++this.ticks % 375 === 0) { // ~1 s (blocos de 128 frames)
      this.port.postMessage({ frames: this.frames, underruns: this.underruns, dropped: this.dropped });
    }
    return true;
  }
}
registerProcessor('pcm-player', PcmPlayer);
`;

// audioHelp explica o que fazer quando a webview não consegue abrir a saída de som.
export function audioHelp(os: string): string {
  if (os === 'linux') {
    return 'Sem som: a saída de áudio do GStreamer não está instalada. Instale e abra o app de novo:\n' +
      '  Ubuntu/Debian: sudo apt install gstreamer1.0-plugins-good\n' +
      '  Fedora: sudo dnf install gstreamer1-plugins-good\n' +
      '  Arch: sudo pacman -S gst-plugins-good';
  }
  return 'Sem som: a webview não conseguiu abrir a saída de áudio do sistema.';
}

export class AudioPlayer {
  private bufferedMs = 0;
  private underruns = 0;
  private droppedMs = 0;
  private received = false;

  private failed = false;

  private constructor(
    private ctx: AudioContext,
    private node: AudioWorkletNode,
    private log: (text: string) => void,
    private onFail: () => void,
  ) {
    ctx.onstatechange = () => log(`áudio: contexto ${ctx.state}`);
    node.port.onmessage = (e) => {
      this.bufferedMs = Math.round((e.data.frames * 1000) / SAMPLE_RATE);
      this.underruns = e.data.underruns;
      this.droppedMs = Math.round((e.data.dropped * 1000) / SAMPLE_RATE);
    };
  }

  // log recebe o que ajuda a diagnosticar (estado do contexto, falhas).
  // onFail é chamado uma vez se a saída de som do sistema não abrir.
  static async create(log: (text: string) => void, onFail: () => void): Promise<AudioPlayer> {
    const ctx = new AudioContext({ sampleRate: SAMPLE_RATE, latencyHint: 'interactive' });
    const url = URL.createObjectURL(new Blob([WORKLET], { type: 'text/javascript' }));
    try {
      await ctx.audioWorklet.addModule(url);
    } finally {
      URL.revokeObjectURL(url);
    }
    const node = new AudioWorkletNode(ctx, 'pcm-player', {
      numberOfInputs: 0,
      outputChannelCount: [CHANNELS],
      processorOptions: { target: (TARGET_MS * SAMPLE_RATE) / 1000, max: (MAX_MS * SAMPLE_RATE) / 1000 },
    });
    node.connect(ctx.destination);
    log(`áudio: contexto ${ctx.state}, ${ctx.sampleRate} Hz, latência ${Math.round((ctx.baseLatency || 0) * 1000)} ms`);
    return new AudioPlayer(ctx, node, log, onFail);
  }

  // pcm: amostras de 16 bits little-endian, intercaladas (esquerda, direita).
  push(pcm: Uint8Array) {
    this.received = true;
    if (this.ctx.state !== 'running') return; // suspenso: não acumula som velho
    const view = new DataView(pcm.buffer, pcm.byteOffset, pcm.byteLength);
    const out = new Float32Array(pcm.byteLength >> 1);
    for (let i = 0; i < out.length; i++) out[i] = view.getInt16(i * 2, true) / 32768;
    this.node.port.postMessage(out, [out.buffer]);
  }

  // A webview pode exigir um gesto do usuário para começar a tocar.
  resume() {
    if (this.ctx.state !== 'suspended' || this.failed) return;
    this.ctx.resume().catch((e) => {
      // Sem gesto do usuário a webview recusa com NotAllowedError, e basta
      // tentar de novo no próximo gesto. Qualquer outro erro é do sistema.
      if (e?.name === 'NotAllowedError') return;
      this.failed = true;
      this.log('áudio: não foi possível tocar: ' + String(e));
      this.onFail();
    });
  }

  stats(): string {
    if (!this.received) return 'áudio: aguardando o celular';
    if (this.failed) return 'áudio: sem saída de som';
    if (this.ctx.state !== 'running') return 'áudio: clique na janela para ouvir';
    return `áudio: fila ${this.bufferedMs} ms  falhas ${this.underruns}  descartado ${this.droppedMs} ms`;
  }

  close() {
    this.node.disconnect();
    this.ctx.close().catch(() => {});
  }
}
