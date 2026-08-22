<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import EnlargeDialog from './EnlargeDialog.vue'

const props = defineProps<{
  graph: string
  id: string
}>()

const svg = ref<string | null>(null)
const error = ref<string | null>(null)
const naturalWidth = ref<number | null>(null)
const enlarge = ref<InstanceType<typeof EnlargeDialog> | null>(null)
let observer: MutationObserver | null = null
let lastTheme: string | null = null
let rendering = false
let renderSeq = 0

// The inline diagram is scaled to the content column (mermaid sets
// width: 100%; max-width: <viewBox width>), which makes wide flowcharts
// unreadable, so every diagram opens in the shared enlarge dialog at its
// natural size.
async function renderChart() {
  if (rendering) return
  rendering = true
  const seq = ++renderSeq
  try {
    const mermaid = (await import('mermaid')).default
    const isDark = document.documentElement.classList.contains('dark')
    const theme = isDark ? 'dark' : 'default'
    if (theme !== lastTheme) {
      mermaid.initialize({
        securityLevel: 'strict',
        startOnLoad: false,
        theme,
      })
      lastTheme = theme
    }
    const { svg: rendered } = await mermaid.render(
      `${props.id}-${seq}`,
      decodeURIComponent(props.graph),
    )
    svg.value = rendered
    error.value = null
    const viewBox = rendered.match(/viewBox="([^"]+)"/)
    const width = viewBox ? parseFloat(viewBox[1].split(/\s+/)[2]) : NaN
    naturalWidth.value = Number.isFinite(width) ? width : null
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to render diagram'
    svg.value = null
  } finally {
    rendering = false
  }
}

function openLightbox() {
  if (svg.value) enlarge.value?.show(svg.value, naturalWidth.value)
}

onMounted(async () => {
  await renderChart()
  observer = new MutationObserver(() => renderChart())
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
})

onUnmounted(() => observer?.disconnect())
</script>

<template>
  <div v-if="error" class="mermaid-error">{{ error }}</div>
  <figure v-else-if="svg" class="mermaid-figure enlargeable">
    <button
      type="button"
      class="mermaid-figure__surface"
      aria-label="Enlarge diagram"
      title="Click to enlarge"
      @click="openLightbox"
    >
      <div class="mermaid-figure__svg" v-html="svg" />
    </button>
    <span class="enlargeable__hint" aria-hidden="true">⤢ Click to enlarge</span>
    <EnlargeDialog ref="enlarge" title="Diagram" />
  </figure>
  <div v-else class="mermaid-loading">Loading diagram...</div>
</template>

<style>
/* Not scoped: the SVG comes from v-html. */
.mermaid-figure {
  margin: 16px 0;
}

.mermaid-figure__surface {
  display: block;
  width: 100%;
  padding: 0;
  border: 1px solid var(--vp-c-divider);
  border-radius: 8px;
  background: var(--vp-c-bg);
  cursor: zoom-in;
  text-align: inherit;
  transition: border-color 0.15s ease;
}

.mermaid-figure__surface:hover,
.mermaid-figure__surface:focus-visible {
  border-color: var(--vp-c-brand-1);
  outline: none;
}

.mermaid-figure__surface:focus-visible {
  box-shadow: 0 0 0 2px var(--vp-c-brand-soft);
}

.mermaid-figure__svg {
  padding: 8px;
  overflow: hidden;
}

.mermaid-figure__svg svg {
  display: block;
  margin: 0 auto;
}

/* The whole figure is the control; the pill is only a hint. */
.mermaid-figure .enlargeable__hint {
  pointer-events: none;
}

@media (prefers-reduced-motion: reduce) {
  .mermaid-figure__surface {
    transition: none;
  }
}
</style>
