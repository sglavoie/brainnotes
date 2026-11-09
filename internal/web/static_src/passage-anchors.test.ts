// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { anchorPassage, passageIndex } from './passage-anchors';

const body = (html: string): HTMLElement => {
  document.body.innerHTML = `<div data-note-body>${html}</div>`;
  return document.querySelector('[data-note-body]')!;
};

it('anchors across inline formatting, Unicode and paragraphs without selecting controls', () => {
  const root = body('<p data-source-line="1">A caf<em>é</em> and a thought.</p><p data-source-line="3">Another paragraph.</p><div class="note-section-copy">Copy section link</div>');
  const index = passageIndex(root);
  expect(anchorPassage(index, 'A cafe\u0301 and a thought.\n\nAnother paragraph.')).toBe(root.querySelectorAll('p')[1]);
  expect(anchorPassage(index, 'Copy section link')).toBeNull();
});

it('keeps a thought attached when earlier paragraphs change, but never guesses after deletion or duplication', () => {
  const root = body('<p data-source-line="1">New introduction.</p><p data-source-line="8">The quoted idea.</p>');
  expect(anchorPassage(passageIndex(root), 'The quoted idea.')).toBe(root.querySelectorAll('p')[1]);
  root.insertAdjacentHTML('beforeend', '<p data-source-line="12">The quoted idea.</p>');
  expect(anchorPassage(passageIndex(root), 'The quoted idea.')).toBeNull();
  expect(anchorPassage(passageIndex(root), 'The deleted idea.')).toBeNull();
});

it('does not confuse an existing annotation or a partial overlapping repeat with the source', () => {
  const root = body('<p data-source-line="1">aaaa</p><details class="passage-annotation-group">A saved thought</details>');
  expect(anchorPassage(passageIndex(root), 'aaa')).toBeNull();
  expect(anchorPassage(passageIndex(root), 'A saved thought')).toBeNull();
});
