import '@testing-library/jest-dom/vitest'
import i18n from './src/lib/i18n'

// Deterministic locale for assertions on rendered copy — production defaults
// to pt-BR, but English strings are easier to read/maintain in test code.
void i18n.changeLanguage('en')
