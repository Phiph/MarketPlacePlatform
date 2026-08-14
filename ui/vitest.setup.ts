import '@testing-library/jest-dom/vitest'

// jsdom doesn't implement these, but Radix UI's Select (used by SchemaForm
// and RequestVersionDialog) calls them during pointer interaction - without
// these no-op polyfills, clicking a Select in a test throws
// "target.hasPointerCapture is not a function" before jsdom even gets to
// render the open dropdown.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}
if (!Element.prototype.releasePointerCapture) {
  Element.prototype.releasePointerCapture = () => {}
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}
