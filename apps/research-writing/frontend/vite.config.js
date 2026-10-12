import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { realpathSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
// The existing campus gateway permits inline styles. Package this small sample's
// own CSS into HTML so the preview works with that unchanged policy.
function inlinePreviewStyles() {
  return {
    name: 'inline-preview-styles',
    enforce: 'post',
    generateBundle(_, bundle) {
      const html = bundle['index.html']
      if (!html || html.type !== 'asset') return
      html.source = String(html.source).replace(/<link\b[^>]*href="\.\/([^"\s]+\.css)"[^>]*>/g, (tag, path) => {
        const css = bundle[path]
        if (!css || css.type !== 'asset') throw new Error(`Missing preview stylesheet: ${path}`)
        return `<style>${String(css.source).replace(/<\/style/gi, '<\\/style')}</style>`
      })
    },
  }
}
export default defineConfig({ root:realpathSync(fileURLToPath(new URL('.', import.meta.url))), plugins: [vue(), inlinePreviewStyles()], base: './', server: { host: '127.0.0.1' } })
