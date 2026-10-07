// TypeScript resolves .ts before .tsx. Names that differ only by case can
// resolve to the wrong module on Windows and default macOS filesystems.
import { readdirSync } from 'node:fs'
import { join, relative } from 'node:path'

const root = process.argv[2] ?? 'src'
const modules = new Map()
function scan(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const file = join(directory, entry.name)
    if (entry.isDirectory()) scan(file)
    else if (/\.tsx?$/.test(entry.name)) {
      const path = relative(root, file).replaceAll('\\', '/')
      const key = path.replace(/\.tsx?$/, '').toLowerCase()
      const previous = modules.get(key)
      if (previous) throw new Error(`Non-portable TypeScript module names: ${previous} and ${path}`)
      modules.set(key, path)
    }
  }
}
scan(root)
console.log(`portable imports: ${modules.size} distinct module paths verified`)
