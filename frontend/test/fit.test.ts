// Roda com "npm test" (node --test, sem dependências; precisa do Node 22.18+).

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { fitSize, toVideoPoint } from '../src/fit.ts';

// Galaxy A71 em pé e deitado, e um monitor 16:9.
const PW = 864, PH = 1920;
const SW = 1920, SH = 1080;

test('fitSize mantém o lado maior da janela e a proporção do vídeo', () => {
  const max = { w: 1920, h: 1040 };
  // Em pé numa janela 1100×800: o lado maior (1100) não cabe na altura da tela.
  assert.deepEqual(fitSize({ w: PW, h: PH }, { w: 1100, h: 800 }, max), { w: 468, h: 1040 });
  // Deitado, a partir dessa janela: o lado maior continua com 1040.
  assert.deepEqual(fitSize({ w: PH, h: PW }, { w: 468, h: 1040 }, max), { w: 1040, h: 468 });
});

test('fitSize não deixa o lado menor abaixo do mínimo da janela, se couber', () => {
  assert.deepEqual(fitSize({ w: PW, h: PH }, { w: 500, h: 500 }, { w: 1920, h: 1040 }), { w: 420, h: 933 });
  // Tela baixa: não cabe, então vale o limite da tela.
  assert.deepEqual(fitSize({ w: PW, h: PH }, { w: 500, h: 500 }, { w: 1280, h: 720 }), { w: 324, h: 720 });
});

test('proporção igual: os três modos dão o mesmo ponto', () => {
  for (const mode of ['cover', 'contain', 'fill'] as const) {
    assert.deepEqual(toVideoPoint(mode, 432, 960, PW, PH, 216, 480), { x: 432, y: 960, inside: true });
  }
});

test('contain: o clique na faixa preta fica fora do vídeo', () => {
  // Celular deitado (20:9) em tela 16:9: faixas em cima e embaixo de 108 px.
  assert.equal(toVideoPoint('contain', SW, SH, PH, PW, 960, 50).inside, false);
  assert.deepEqual(toVideoPoint('contain', SW, SH, PH, PW, 960, 540), { x: 960, y: 432, inside: true });
  assert.deepEqual(toVideoPoint('contain', SW, SH, PH, PW, 0, 108), { x: 0, y: 0, inside: true });
});

test('cover: o canto da janela cai dentro do vídeo, não na borda dele', () => {
  // Escala 1080/864 = 1,25: o vídeo fica com 2400 px e perde 240 de cada lado.
  assert.deepEqual(toVideoPoint('cover', SW, SH, PH, PW, 0, 0), { x: 192, y: 0, inside: true });
  assert.deepEqual(toVideoPoint('cover', SW, SH, PH, PW, 960, 540), { x: 960, y: 432, inside: true });
  assert.deepEqual(toVideoPoint('cover', SW, SH, PH, PW, 1919, 1079), { x: 1727, y: 863, inside: true });
});

test('fill: cada eixo tem a sua escala', () => {
  assert.deepEqual(toVideoPoint('fill', SW, SH, PH, PW, 0, 0), { x: 0, y: 0, inside: true });
  assert.deepEqual(toVideoPoint('fill', SW, SH, PH, PW, 960, 540), { x: 960, y: 432, inside: true });
  assert.deepEqual(toVideoPoint('fill', SW, SH, PH, PW, 1919, 1079), { x: 1919, y: 863, inside: true });
});

test('ponto fora da caixa é limitado ao vídeo', () => {
  assert.deepEqual(toVideoPoint('cover', 432, 960, PW, PH, -10, 5000), { x: 0, y: 1919, inside: false });
});
