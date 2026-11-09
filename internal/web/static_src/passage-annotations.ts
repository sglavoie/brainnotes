import { anchorPassage, passageIndex } from './passage-anchors';
import { readableExcerpt } from './note-excerpt';

export type SavedPassageThought = { source: string; quote: string; path: string; title: string; thought: string };

export const initPassageAnnotations = (body: HTMLElement): void => {
  const fallback = document.querySelector<HTMLElement>('[data-passage-thoughts]');
  if (!fallback) return;
  const groups = new Map<HTMLElement, HTMLDetailsElement>();
  const paths = new Set<string>();
  let index: ReturnType<typeof passageIndex> | undefined;
  const place = (record: HTMLElement): void => {
    const path = record.querySelector<HTMLAnchorElement>('a')!.href;
    if (paths.has(path)) { record.remove(); return; }
    paths.add(path);
    prepareAnnotation(record);
    index ||= passageIndex(body);
    const anchor = anchorPassage(index, record.dataset.quote || '');
    if (!anchor || anchor === body) { fallback.append(record); fallback.hidden = false; return; }
    let group = groups.get(anchor);
    if (!group) {
      group = document.createElement('details'); group.className = 'passage-annotation-group';
      group.append(document.createElement('summary'));
      // A heading lives inside its copy-control wrapper; keep annotations out
      // of both that wrapper and the heading's accessible name.
      (anchor.closest('.note-section-heading') || anchor).after(group);
      groups.set(anchor, group);
    }
    group.append(record);
    record.querySelector('blockquote')?.setAttribute('hidden', '');
    const count = group.querySelectorAll('[data-passage-annotation]').length;
    group.querySelector('summary')!.textContent = count === 1 ? 'Your thought' : `Your thoughts · ${count}`;
    fallback.hidden = !fallback.querySelector('[data-passage-annotation]');
  };
  for (const record of fallback.querySelectorAll<HTMLElement>('[data-passage-annotation]')) place(record);
  document.addEventListener('bn:thought-saved', (event) => {
    const thought = (event as CustomEvent<SavedPassageThought>).detail;
    if (thought.source.toLocaleLowerCase() !== body.dataset.noteKey?.toLocaleLowerCase()) return;
    const record = document.createElement('article'); record.className = 'passage-annotation';
    record.dataset.passageAnnotation = ''; record.dataset.quote = thought.quote;
    const link = document.createElement('a'); link.href = thought.path; link.textContent = thought.title;
    const text = document.createElement('p'); text.textContent = thought.thought.slice(0, 240);
    const quote = document.createElement('blockquote'); quote.textContent = thought.quote;
    record.append(link, text, quote); place(record);
    record.closest('details')?.classList.add('is-new-thought');
  });
};

const prepareAnnotation = (record: HTMLElement): void => {
    const link = record.querySelector<HTMLAnchorElement>('a')!;
    const beside = document.createElement('button');
    beside.type = 'button'; beside.className = 'bn-button'; beside.textContent = 'Read beside this passage';
    beside.addEventListener('click', () => document.dispatchEvent(new CustomEvent('bn:passage-reference', { detail: { path: link.href, target: beside } })));
    record.append(beside);
    const prose = record.querySelector('p');
    if (prose) prose.textContent = readableExcerpt(prose.textContent || '');
};

const body = document.querySelector<HTMLElement>('[data-note-body]');
if (body) initPassageAnnotations(body);
