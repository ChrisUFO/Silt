// Vitest setup: registers @testing-library/jest-dom matchers
// (toBeInTheDocument, toHaveAttribute, …) on the vitest `expect`. The
// `/vitest` entry point imports expect from vitest itself, so this works
// with globals: false (unlike the plain `@testing-library/jest-dom`
// import, which expects a global expect).
import '@testing-library/jest-dom/vitest'
