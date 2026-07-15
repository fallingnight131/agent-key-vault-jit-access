import { readdir, readFile } from 'node:fs/promises'
import { extname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../src', import.meta.url))
const forbidden = [
  ['v-html', 'unescaped Vue HTML rendering'],
  ['localStorage', 'persistent browser storage'],
  ['sessionStorage', 'persistent browser storage'],
  ['indexedDB', 'persistent browser storage'],
  ['.innerHTML', 'direct HTML rendering'],
  ['.outerHTML', 'direct HTML rendering'],
  ['document.write', 'direct document rendering'],
  ['console.', 'browser console logging'],
  ['window.prompt', 'browser-native prompt dialog'],
  ['window.confirm', 'browser-native confirmation dialog'],
  ['window.alert', 'browser-native alert dialog'],
]

async function sourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) files.push(...await sourceFiles(path))
    else if (['.js', '.vue'].includes(extname(entry.name))) files.push(path)
  }
  return files
}

const violations = []
for (const file of await sourceFiles(root)) {
  const source = await readFile(file, 'utf8')
  for (const [token, reason] of forbidden) {
    if (source.includes(token)) violations.push(`${file}: ${reason} (${token})`)
  }
}

if (violations.length > 0) {
  throw new Error(`AKV Web security check failed:\n${violations.join('\n')}`)
}
