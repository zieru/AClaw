import fs from 'fs'
import path from 'path'
import zlib from 'zlib'
import { fileURLToPath } from 'url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const targetDir = path.resolve(__dirname, '../internal/webadmin/ui')
const compressExtensions = new Set(['.html', '.js', '.css', '.json', '.svg', '.ttf', '.eot', '.xml'])

function compressDirectory(dir) {
  if (!fs.existsSync(dir)) return

  const entries = fs.readdirSync(dir, { withFileTypes: true })
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      compressDirectory(fullPath)
    } else if (entry.isFile() && !entry.name.endsWith('.gz')) {
      const ext = path.extname(entry.name).toLowerCase()
      if (compressExtensions.has(ext)) {
        try {
          const content = fs.readFileSync(fullPath)
          const gzipped = zlib.gzipSync(content, { level: 9 })
          const gzPath = fullPath + '.gz'
          fs.writeFileSync(gzPath, gzipped)
          console.log(`[gzip] Compressed: ${path.relative(targetDir, fullPath)} (${content.length} -> ${gzipped.length} bytes)`)
        } catch (err) {
          console.error(`[gzip] Error compressing ${fullPath}:`, err.message)
        }
      }
    }
  }
}

console.log(`📦 Pre-compressing static assets in ${targetDir}...`)
compressDirectory(targetDir)
console.log('✅ Pre-compression completed!')
