// Tamanho da área de vídeo para a janela ficar com a mesma proporção do
// celular, sem faixas pretas em volta, e a conta de coordenadas do clique.

export interface Size {
  w: number;
  h: number;
}

// Menor lado que a janela aceita (MinWidth/MinHeight em main.go).
export const MIN_SIDE = 420;

// fitSize escala o vídeo para caber em max. O lado maior do vídeo fica com o
// tamanho do lado maior da área atual, para a janela não crescer nem encolher
// à toa quando o celular gira. Se o lado menor ficar abaixo do mínimo da
// janela, aumenta a escala: senão a janela pararia no mínimo e sobraria faixa.
export function fitSize(video: Size, current: Size, max: Size): Size {
  const long = Math.max(current.w, current.h);
  const wanted = Math.max(long / Math.max(video.w, video.h), MIN_SIDE / Math.min(video.w, video.h));
  const scale = Math.min(wanted, max.w / video.w, max.h / video.h);
  return { w: Math.round(video.w * scale), h: Math.round(video.h * scale) };
}

// Como o vídeo ocupa a janela quando as proporções não batem (igual ao
// object-fit do CSS): 'cover' enche e corta as sobras, 'contain' mostra tudo
// com faixas pretas, 'fill' enche esticando.
export type FitMode = 'cover' | 'contain' | 'fill';

export const FIT_MODES: FitMode[] = ['cover', 'contain', 'fill'];

// toVideoPoint converte um ponto da área de vídeo (em pixels da janela, a
// partir do canto da área) para o frame. "inside" diz se caiu em cima do vídeo.
export function toVideoPoint(mode: FitMode, boxW: number, boxH: number, videoW: number, videoH: number, px: number, py: number) {
  let sx = boxW / videoW;
  let sy = boxH / videoH;
  if (mode === 'contain') sx = sy = Math.min(sx, sy);
  else if (mode === 'cover') sx = sy = Math.max(sx, sy);
  const x = Math.round((px - (boxW - videoW * sx) / 2) / sx);
  const y = Math.round((py - (boxH - videoH * sy) / 2) / sy);
  return {
    x: Math.max(0, Math.min(videoW - 1, x)),
    y: Math.max(0, Math.min(videoH - 1, y)),
    inside: x >= 0 && y >= 0 && x < videoW && y < videoH,
  };
}
