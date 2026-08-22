<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRoute } from 'vitepress'
import EnlargeDialog from './EnlargeDialog.vue'

// Mounted in the doc-after slot: finds every markdown table on the page
// and adds an "Enlarge" pill that opens the table in the shared dialog,
// where it gets the whole viewport instead of the 688px scroll box.
const route = useRoute()
const enlarge = ref<InstanceType<typeof EnlargeDialog> | null>(null)
const buttons: HTMLButtonElement[] = []

function openTable(table: HTMLTableElement) {
  enlarge.value?.show(table.outerHTML)
}

function enhance() {
  const tables = document.querySelectorAll<HTMLTableElement>('.vp-doc table')
  tables.forEach((table) => {
    if (table.parentElement?.classList.contains('enlargeable--table')) return
    const wrap = document.createElement('div')
    wrap.className = 'enlargeable enlargeable--table'
    table.parentNode?.insertBefore(wrap, table)
    wrap.appendChild(table)
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'enlargeable__hint'
    button.textContent = '⤢ Enlarge'
    button.setAttribute('aria-label', 'Enlarge table')
    button.addEventListener('click', () => openTable(table))
    wrap.appendChild(button)
    buttons.push(button)
  })
}

async function refresh() {
  await nextTick()
  enhance()
}

onMounted(refresh)
watch(() => route.path, refresh)
onUnmounted(() => {
  buttons.length = 0
})
</script>

<template>
  <EnlargeDialog ref="enlarge" title="Table" />
</template>
