// Teste do decodificador na inicialização. No Linux, o WebKitGTK diz que
// suporta H.264 mesmo sem o plugin do GStreamer e só falha na hora de
// decodificar, então o único teste confiável é decodificar um frame de verdade.

// Keyframe H.264 Baseline de 64x64 (SPS + PPS + IDR, 68 bytes), gerado com o ffmpeg.
const TEST_FRAME =
  'AAAAAWdCwAraEJsBEAAAAwAQAAADACDxImoAAAABaM4PyAAAAWWIhDoRigACEXHAAEECOAAIGsnJyddddddddddddeA=';

export interface SelfTest {
  ok: boolean;
  detail: string;
}

export async function decoderSelfTest(): Promise<SelfTest> {
  if (typeof VideoDecoder === 'undefined') {
    return { ok: false, detail: 'Esta webview não tem WebCodecs (VideoDecoder).' };
  }
  const data = Uint8Array.from(atob(TEST_FRAME), (c) => c.charCodeAt(0));
  return new Promise<SelfTest>((resolve) => {
    let done = false;
    const finish = (r: SelfTest) => {
      if (done) return;
      done = true;
      try { dec.close(); } catch { /* já fechado */ }
      resolve(r);
    };
    const dec = new VideoDecoder({
      output: (frame) => {
        frame.close();
        finish({ ok: true, detail: 'H.264 decodificado com sucesso' });
      },
      error: (e) => finish({ ok: false, detail: String(e) }),
    });
    try {
      dec.configure({ codec: 'avc1.42C00A', optimizeForLatency: true });
      dec.decode(new EncodedVideoChunk({ type: 'key', timestamp: 0, data }));
      dec.flush().catch((e) => finish({ ok: false, detail: String(e) }));
    } catch (e) {
      finish({ ok: false, detail: String(e) });
    }
    setTimeout(() => finish({ ok: false, detail: 'tempo esgotado decodificando o frame de teste' }), 4000);
  });
}

export function decoderHelp(os: string): string {
  if (os === 'linux') {
    return 'O decodificador H.264 do GStreamer não está instalado. Instale e abra o app de novo:\n' +
      '  Ubuntu/Debian: sudo apt install gstreamer1.0-libav\n' +
      '  Fedora: sudo dnf install gstreamer1-plugin-openh264 (ou gstreamer1-plugin-libav do RPM Fusion)\n' +
      '  Arch: sudo pacman -S gst-libav';
  }
  return 'A webview não conseguiu decodificar H.264. Atualize o Microsoft Edge WebView2 Runtime.';
}
