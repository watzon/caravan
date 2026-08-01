import { describe, expect, it } from 'vitest';
import { formatCategories, parseCategories, validateIndexer } from './indexer';

describe('parseCategories', () => {
  it('reads the comma-separated form indexers document', () => {
    expect(parseCategories('2000,2040,5000')).toEqual([2000, 2040, 5000]);
  });

  it('tolerates spaces and a trailing comma', () => {
    expect(parseCategories(' 2000 , 5000, ')).toEqual([2000, 5000]);
  });

  it('drops duplicates so an edit round-trips stably', () => {
    expect(parseCategories('2000,2000,5000')).toEqual([2000, 5000]);
  });

  it('drops anything that is not a category id', () => {
    expect(parseCategories('2000,abc,-5,1.5')).toEqual([2000]);
  });

  it('reads an empty field as no categories', () => {
    expect(parseCategories('')).toEqual([]);
    expect(parseCategories('   ')).toEqual([]);
  });
});

describe('formatCategories', () => {
  it('renders stored ids back into the editable form', () => {
    expect(formatCategories([2000, 5000])).toBe('2000, 5000');
  });

  it('renders missing categories as an empty field', () => {
    expect(formatCategories([])).toBe('');
    expect(formatCategories(null)).toBe('');
    expect(formatCategories(undefined)).toBe('');
  });

  it('round-trips through parseCategories', () => {
    expect(parseCategories(formatCategories([2000, 2040]))).toEqual([2000, 2040]);
  });
});

describe('validateIndexer', () => {
  it('accepts a complete config', () => {
    expect(validateIndexer({ name: 'Jackett', url: 'http://127.0.0.1:9117/api' })).toBeNull();
    expect(validateIndexer({ name: 'Jackett', url: 'https://example.test' })).toBeNull();
  });

  it('requires a name', () => {
    expect(validateIndexer({ name: '  ', url: 'https://example.test' })).toMatch(/name/i);
  });

  it('requires a URL', () => {
    expect(validateIndexer({ name: 'Jackett', url: '' })).toMatch(/URL/i);
  });

  it('rejects a URL without a scheme, which fetches as a relative path', () => {
    expect(validateIndexer({ name: 'Jackett', url: '127.0.0.1:9117' })).toMatch(/http/i);
  });
});
