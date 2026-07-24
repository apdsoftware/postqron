import eslint from '@eslint/js'
import tseslint from 'typescript-eslint'
import vue from 'eslint-plugin-vue'

export default tseslint.config(
  {
    ignores: [
      '**/.nuxt/**',
      '**/.output/**',
      '**/coverage/**',
      '**/dist/**',
      '**/node_modules/**',
    ],
  },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs['flat/recommended'],
  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
      },
    },
    rules: {
      'vue/multi-word-component-names': 'off',
    },
  },
  {
    files: ['**/*.{js,mjs,ts,vue}'],
    rules: {
      'no-console': ['error', { allow: ['warn', 'error'] }],
      'semi': ['error', 'never'],
    },
  },
  {
    files: [
      'features/f23-pwa/**/*.js',
      'features/f23-pwa/**/*.mjs',
    ],
    languageOptions: {
      globals: {
        Buffer: 'readonly',
        URL: 'readonly',
        caches: 'readonly',
        fetch: 'readonly',
        self: 'readonly',
      },
    },
    rules: {
      'no-useless-assignment': 'off',
      'semi': ['error', 'always'],
    },
  },
)
