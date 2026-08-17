import { describe, expect, it } from 'vitest';

import { formatDefaultValue, isUnsetValue } from '../defaultValue';

describe('isUnsetValue', () => {
  it('treats undefined/null/empty string as unset', () => {
    expect(isUnsetValue(undefined)).toBe(true);
    expect(isUnsetValue(null)).toBe(true);
    expect(isUnsetValue('')).toBe(true);
  });

  it('treats 0/false/non-empty values as set', () => {
    expect(isUnsetValue(0)).toBe(false);
    expect(isUnsetValue(false)).toBe(false);
    expect(isUnsetValue('0.7')).toBe(false);
    expect(isUnsetValue(0.7)).toBe(false);
  });
});

describe('formatDefaultValue', () => {
  it('returns null for unset values (nothing to show)', () => {
    expect(formatDefaultValue(undefined)).toBeNull();
    expect(formatDefaultValue(null)).toBeNull();
    expect(formatDefaultValue('')).toBeNull();
  });

  it('formats booleans as 开/关', () => {
    expect(formatDefaultValue(true)).toBe('开');
    expect(formatDefaultValue(false)).toBe('关');
  });

  it('formats numbers and strings verbatim', () => {
    expect(formatDefaultValue(0.7)).toBe('0.7');
    expect(formatDefaultValue(5)).toBe('5');
    expect(formatDefaultValue('hybrid')).toBe('hybrid');
  });
});
