import { readdirSync, readFileSync } from 'node:fs';
import { relative, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

const sourceRoot = resolve(process.cwd(), 'src');
const sourceFilePattern = /\.(?:ts|tsx)$/;
const testFilePattern = /(?:\.test|\.spec)\.(?:ts|tsx)$/;
const duplicatedPrefixPattern =
  /(?:\b(?:api|client)\.(?:get|post|put|patch|delete)|\bfetch)\(\s*(['"`])(\/api\/[^'"`\s]*)\1/g;

const listSourceFiles = (directory: string): string[] => readdirSync(directory, { withFileTypes: true })
  .flatMap((entry) => {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) return listSourceFiles(path);
    if (!sourceFilePattern.test(entry.name) || testFilePattern.test(entry.name)) return [];
    return [path];
  });

describe('frontend API request paths', () => {
  it('does not duplicate the Nginx-owned /api prefix', () => {
    const violations = listSourceFiles(sourceRoot).flatMap((file) => {
      const source = readFileSync(file, 'utf8');
      return Array.from(source.matchAll(duplicatedPrefixPattern), (match) => (
        `${relative(sourceRoot, file)}: ${match[2]}`
      ));
    });

    expect(violations).toEqual([]);
  });
});
