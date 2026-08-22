<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRoute } from 'vitepress'
import EnlargeDialog from './EnlargeDialog.vue'

// Mounted in the doc-after slot: gives markdown tables and fenced code
// blocks an "Enlarge" pill that opens them in the shared dialog, where
// they get the whole viewport instead of the 688px scroll box. Mermaid
// diagrams do the same through Mermaid.vue. Runs again on route change.
//
// Code blocks are enhanced only when they overflow sideways or are long;
// a three-line snippet does not need it and the pill would be noise.
const CODE_MIN_LINES = 15

const route = useRoute()
const enlarge = ref<InstanceType<typeof EnlargeDialog> | null>(null)
const buttons: HTMLButtonElement[] = []

function makeButton(label: string, onClick: () => void, extraClass = ''): HTMLButtonElement {
  const button = document.createElement('button')
  button.type = 'button'
  button.className = `enlargeable__hint ${extraClass}`.trim()
  button.textContent = '⤢ Enlarge'
  button.setAttribute('aria-label', label)
  button.addEventListener('click', onClick)
  buttons.push(button)
  return button
}

function enhanceTables() {
  document.querySelectorAll<HTMLTableElement>('.vp-doc table').forEach((table) => {
    if (table.parentElement?.classList.contains('enlargeable--table')) return
    const wrap = document.createElement('div')
    wrap.className = 'enlargeable enlargeable--table'
    table.parentNode?.insertBefore(wrap, table)
    wrap.appendChild(table)
    wrap.appendChild(makeButton('Enlarge table', () => enlarge.value?.show(table.outerHTML)))
  })
}

function enhanceCodeBlocks() {
  document.querySelectorAll<HTMLElement>(".vp-doc div[class*='language-']").forEach((block) => {
    if (block.classList.contains('enlargeable')) return
    if (block.closest('.enlarge-dialog')) return
    const pre = block.querySelector('pre')
    if (!pre) return
    const lines = (pre.textContent ?? '').split('\n').length
    const overflows = pre.scrollWidth > pre.clientWidth + 1
    if (!overflows && lines < CODE_MIN_LINES) return
    block.classList.add('enlargeable', 'enlargeable--code')
    block.appendChild(
      makeButton(
        'Enlarge code block',
        () => {
          const clone = block.cloneNode(true) as HTMLElement
          clone.classList.remove('enlargeable', 'enlargeable--code')
          clone.querySelectorAll('button').forEach((b) => b.remove())
          enlarge.value?.show(clone.outerHTML)
        },
        'enlargeable__hint--code',
      ),
    )
  })
}

async function refresh() {
  await nextTick()
  enhanceTables()
  enhanceCodeBlocks()
}

onMounted(refresh)
watch(() => route.path, refresh)
onUnmounted(() => {
  buttons.length = 0
})
</script>

<template>
  <EnlargeDialog ref="enlarge" title="Content" />
</template>
