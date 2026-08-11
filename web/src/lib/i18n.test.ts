import { describe, expect, it } from 'vitest';
import { createI18n, translate } from './i18n.svelte';

describe('i18n', () => {
  it('reads English messages from the locale catalog', () => {
    expect(translate('app.title.discover')).toBe('Discover');
  });

  it('interpolates named values without assuming word order', () => {
    const i18n = createI18n('en');

    expect(i18n.t('common.pagination.pageOf', { page: 2, pages: 5 })).toBe('Page 2 of 5');
  });

  it('selects the locale plural form', () => {
    const i18n = createI18n('en');

    expect(i18n.tp('common.count.item', 1)).toBe('1 item');
    expect(i18n.tp('common.count.item', 3)).toBe('3 items');
    expect(i18n.tp('common.count.item', 1234)).toBe('1,234 items');
  });

  it('keeps rich placeholders as ordered message parts', () => {
    const i18n = createI18n('en');

    expect(i18n.parts('common.pagination.pageOf', { page: 2 })).toEqual([
      'Page ',
      '2',
      ' of ',
      { placeholder: 'pages' },
    ]);
  });
});
