module.exports = {
  root: true,
  env: { browser: true, es2022: true, node: true },
  parser: '@typescript-eslint/parser',
  parserOptions: {
    ecmaVersion: 2022,
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
    project: './tsconfig.json',
  },
  settings: {
    react: { version: '18' },
    'import/resolver': {
      typescript: { project: './tsconfig.json' },
      node: true,
    },
  },
  plugins: ['react', 'react-hooks', 'import', '@typescript-eslint'],
  extends: [
    'eslint:recommended',
    'plugin:react/recommended',
    'plugin:react-hooks/recommended',
    'plugin:@typescript-eslint/recommended',
    'plugin:import/recommended',
    'plugin:import/typescript',
  ],
  rules: {
    'react/react-in-jsx-scope': 'off',
    'react/prop-types': 'off',
    '@typescript-eslint/no-explicit-any': 'off',
    '@typescript-eslint/no-unused-vars': ['warn', { argsIgnorePattern: '^_' }],
    'import/order': [
      'warn',
      { 'newlines-between': 'always', alphabetize: { order: 'asc' } },
    ],
    'import/no-restricted-paths': [
      'error',
      {
        zones: [
          // 模块互斥（iam 作为认证横切关注点，agent 作为编排层 — 都允许被依赖；agent 自身可依赖其他业务模块）
          ...['skill', 'mcp', 'knowledge', 'memory'].flatMap((from) =>
            ['skill', 'mcp', 'knowledge', 'memory']
              .filter((to) => to !== from)
              .map((to) => ({
                target: `./src/modules/${from}`,
                from: `./src/modules/${to}`,
                message: `禁止跨模块导入: ${from} -> ${to}`,
              })),
          ),
          // iam 不能依赖业务模块
          ...['agent', 'skill', 'mcp', 'knowledge', 'memory'].map((to) => ({
            target: './src/modules/iam',
            from: `./src/modules/${to}`,
            message: `iam 禁止依赖业务模块: iam -> ${to}`,
          })),
          // shared 不能反向依赖 modules
          {
            target: './src/shared',
            from: './src/modules',
            message: 'shared/ 禁止依赖 modules/',
          },
          // 跨模块只能走 index.ts —— 禁止深路径引用
          ...['agent', 'skill', 'mcp', 'knowledge', 'memory', 'iam', 'dashboard'].flatMap((m) =>
            ['agent', 'skill', 'mcp', 'knowledge', 'memory', 'iam', 'dashboard']
              .filter((other) => other !== m)
              .flatMap((other) =>
                ['api', 'hooks', 'components', 'pages', 'model'].map((layer) => ({
                  target: `./src/modules/${m}`,
                  from: `./src/modules/${other}/${layer}`,
                  message: `跨模块禁止深路径引用，请改走 @/modules/${other}（index.ts）`,
                })),
              ),
          ),
          // 模块内分层（单向 model → api → hooks → components → pages）
          ...['agent', 'skill', 'mcp', 'knowledge', 'memory', 'iam', 'dashboard'].flatMap((m) => [
            // model 不能依赖 api/hooks/components/pages
            {
              target: `./src/modules/${m}/model`,
              from: `./src/modules/${m}/api`,
              message: 'model/ 禁止依赖 api/',
            },
            {
              target: `./src/modules/${m}/model`,
              from: `./src/modules/${m}/hooks`,
              message: 'model/ 禁止依赖 hooks/',
            },
            {
              target: `./src/modules/${m}/model`,
              from: `./src/modules/${m}/components`,
              message: 'model/ 禁止依赖 components/',
            },
            {
              target: `./src/modules/${m}/model`,
              from: `./src/modules/${m}/pages`,
              message: 'model/ 禁止依赖 pages/',
            },
            // api 不能依赖 hooks/components/pages
            {
              target: `./src/modules/${m}/api`,
              from: `./src/modules/${m}/hooks`,
              message: 'api/ 禁止依赖 hooks/',
            },
            {
              target: `./src/modules/${m}/api`,
              from: `./src/modules/${m}/components`,
              message: 'api/ 禁止依赖 components/',
            },
            {
              target: `./src/modules/${m}/api`,
              from: `./src/modules/${m}/pages`,
              message: 'api/ 禁止依赖 pages/',
            },
            // hooks 不能依赖 components/pages
            {
              target: `./src/modules/${m}/hooks`,
              from: `./src/modules/${m}/components`,
              message: 'hooks/ 禁止依赖 components/',
            },
            {
              target: `./src/modules/${m}/hooks`,
              from: `./src/modules/${m}/pages`,
              message: 'hooks/ 禁止依赖 pages/',
            },
            // components 不能依赖 pages
            {
              target: `./src/modules/${m}/components`,
              from: `./src/modules/${m}/pages`,
              message: 'components/ 禁止依赖 pages/',
            },
          ]),
        ],
      },
    ],
    'no-restricted-imports': [
      'error',
      {
        patterns: [
          {
            group: [
              '@/contexts/*',
              '@/hooks/*',
              '@/pages/*',
              '@/components/*',
              '@/utils/*',
              '@/services/api',
              '@/services/index',
              '@/services/auth',
              '@/services/tenant',
              '@/services/skills',
              '@/services/agents',
              '@/services/conversations',
              '@/services/memory',
              '@/services/mcp',
              '@/services/knowledge',
            ],
            message: '旧目录已废弃，使用 @/modules/<domain> 或 @/shared/* 替代',
          },
        ],
      },
    ],
    'no-console': ['error', { allow: ['warn', 'error'] }],
  },
  ignorePatterns: [
    'dist',
    'node_modules',
    'vite.config.*',
    'vitest.config.*',
  ],
};
