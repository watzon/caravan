export const FIELD_ACCESSIBILITY_CONTEXT = Symbol('field-accessibility');

export interface FieldAccessibilityContext {
  readonly describedBy: string | undefined;
  readonly invalid: boolean;
}
