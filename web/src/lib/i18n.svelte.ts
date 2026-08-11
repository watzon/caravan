import { getContext, setContext } from 'svelte';
import appMessages from '../locales/en/app.json';
import componentMessages from '../locales/en/components.json';
import secondaryComponentMessages from '../locales/en/components-secondary.json';
import routeMessages from '../locales/en/routes.json';
import secondaryRouteMessages from '../locales/en/routes-secondary.json';
import tertiaryRouteMessages from '../locales/en/routes-tertiary.json';

const messages = {
  ...appMessages,
  ...componentMessages,
  ...secondaryComponentMessages,
  ...routeMessages,
  ...secondaryRouteMessages,
  ...tertiaryRouteMessages,
} as const;

export type Locale = 'en';
export type TranslationKey = keyof typeof messages;
type PluralSuffix = '.zero' | '.one' | '.two' | '.few' | '.many' | '.other';
type WithoutPluralSuffix<Key> = Key extends `${infer Base}${PluralSuffix}` ? Base : never;
export type PluralTranslationKey = WithoutPluralSuffix<TranslationKey>;
export type TranslationValues = Record<string, string | number>;
export type Translator = (key: TranslationKey, values?: TranslationValues) => string;
export type PluralTranslator = (
  key: PluralTranslationKey,
  count: number,
  values?: TranslationValues,
) => string;
export type MessagePart = string | { placeholder: string };
export type MessagePartsFormatter = (
  key: TranslationKey,
  values?: TranslationValues,
) => MessagePart[];

export interface I18n {
  readonly locale: Locale;
  readonly t: Translator;
  readonly tp: PluralTranslator;
  readonly parts: MessagePartsFormatter;
  setLocale(locale: Locale): void;
}

const languageTags: Record<Locale, string> = {
  en: 'en-GB',
};
const I18N_CONTEXT = Symbol('caravan.i18n');
const dictionaries: Record<Locale, Readonly<Record<string, string>>> = {
  en: messages,
};

function formatMessage(template: string, values: TranslationValues): string {
  return template.replace(/\{([A-Za-z][A-Za-z0-9_]*)\}/g, (placeholder, name: string) => {
    const value = values[name];
    return value === undefined ? placeholder : String(value);
  });
}

function formatMessageParts(
  template: string,
  values: TranslationValues,
): MessagePart[] {
  const parts: MessagePart[] = [];
  let cursor = 0;

  for (const match of template.matchAll(/\{([A-Za-z][A-Za-z0-9_]*)\}/g)) {
    const index = match.index;
    if (index > cursor) parts.push(template.slice(cursor, index));
    const name = match[1]!;
    const value = values[name];
    parts.push(value === undefined ? { placeholder: name } : String(value));
    cursor = index + match[0].length;
  }

  if (cursor < template.length) parts.push(template.slice(cursor));
  return parts;
}

export function createI18n(initialLocale: Locale = 'en'): I18n {
  let locale = $state(initialLocale);

  return {
    get locale() {
      return locale;
    },
    t(key, values = {}) {
      const template = dictionaries[locale][key];
      if (template === undefined) throw new Error(`Missing message: ${key}`);
      return formatMessage(template, values);
    },
    parts(key, values = {}) {
      const template = dictionaries[locale][key];
      if (template === undefined) throw new Error(`Missing message: ${key}`);
      return formatMessageParts(template, values);
    },
    tp(key, count, values = {}) {
      const dictionary = dictionaries[locale];
      const languageTag = languageTags[locale];
      const category = new Intl.PluralRules(languageTag).select(count);
      const template = dictionary[`${key}.${category}`] ?? dictionary[`${key}.other`];
      if (template === undefined) throw new Error(`Missing plural message: ${key}`);
      return formatMessage(template, {
        ...values,
        count: new Intl.NumberFormat(languageTag).format(count),
      });
    },
    setLocale(nextLocale) {
      locale = nextLocale;
    },
  };
}

const defaultI18n = createI18n();

export function provideI18n(locale: Locale = 'en'): I18n {
  defaultI18n.setLocale(locale);
  setContext(I18N_CONTEXT, defaultI18n);
  return defaultI18n;
}

export function useI18n(): I18n {
  return getContext<I18n | undefined>(I18N_CONTEXT) ?? defaultI18n;
}

export function translate(key: TranslationKey, values?: TranslationValues): string {
  return defaultI18n.t(key, values);
}

export function translatePlural(
  key: PluralTranslationKey,
  count: number,
  values?: TranslationValues,
): string {
  return defaultI18n.tp(key, count, values);
}

export function currentLocale(): string {
  return languageTags[defaultI18n.locale];
}
