<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'

const props = defineProps<{
  graph: string
  id: string
}>()

const svg = ref<string | null>(null)
const error = ref<string | null>(null)
const naturalWidth = ref<number | null>(null)
const dialog = ref<HTMLDialogElement | null>(null)
const open = ref(false)
const fit = ref(false)
let observer: MutationObserver | null = null
let lastTheme: string | null = null
let rendering = false
let renderSeq = 0

// The inline diagram is scaled to the content column (mermaid sets
// width: 100%; max-width: <viewBox width>), which makes wide flowcharts
// unreadable. The lightbox shows the same SVG at its natural size, with
// a fit-to-screen toggle for an overview.
const lightboxStyle = computed(() => {
  if (fit.value || !naturalWidth.value) return {}
  return { '--mermaid-natural-width': `${Math.round(naturalWidth.value)}px` }
})

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
  if (!svg.value || !dialog.value) return
  fit.value = false
  open.value = true
  dialog.value.showModal()
}

function closeLightbox() {
  open.value = false
  dialog.value?.close()
}

function onBackdropClick(e: MouseEvent) {
  if (e.target === dialog.value) closeLightbox()
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
  <figure v-else-if="svg" class="mermaid-figure">
    <button
      type="button"
      class="mermaid-figure__surface"
      :aria-label="'Enlarge diagram'"
      title="Click to enlarge"
      @click="openLightbox"
    >
      <div class="mermaid-figure__svg" v-html="svg" />
    </button>
    <span class="mermaid-figure__hint" aria-hidden="true">⤢ Click to enlarge</span>
    <dialog
      ref="dialog"
      class="mermaid-lightbox"
      aria-label="Diagram, enlarged"
      :style="lightboxStyle"
      :data-fit="fit ? 'true' : 'false'"
      @click="onBackdropClick"
      @close="open = false"
    >
      <div class="mermaid-lightbox__bar">
        <span class="mermaid-lightbox__title">Diagram</span>
        <button type="button" class="mermaid-lightbox__btn" :aria-pressed="fit" @click="fit = !fit">
          {{ fit ? 'Actual size' : 'Fit to screen' }}
        </button>
        <button type="button" class="mermaid-lightbox__btn" @click="closeLightbox">Close</button>
      </div>
      <div v-if="open" class="mermaid-lightbox__pane" v-html="svg" />
    </dialog>
  </figure>
  <div v-else class="mermaid-loading">Loading diagram...</div>
</template>

<style>
/* Not scoped: the SVG comes from v-html, so scoped attributes would not
   reach it. Selectors are namespaced under .mermaid-* instead. */
.mermaid-figure {
  position: relative;
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

.mermaid-figure__hint {
  position: absolute;
  top: 8px;
  right: 8px;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--vp-c-bg-soft);
  color: var(--vp-c-text-2);
  font-size: 12px;
  line-height: 18px;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.mermaid-figure:hover .mermaid-figure__hint,
.mermaid-figure__surface:focus-visible + .mermaid-figure__hint {
  opacity: 1;
}

@media (hover: none) {
  .mermaid-figure__hint {
    opacity: 1;
  }
}

.mermaid-lightbox {
  width: min(96vw, 1800px);
  max-width: none;
  height: 92vh;
  max-height: none;
  padding: 0;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  background: var(--vp-c-bg);
  color: var(--vp-c-text-1);
  overflow: hidden;
}

.mermaid-lightbox::backdrop {
  background: rgb(0 0 0 / 55%);
}

.mermaid-lightbox__bar {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 8px 12px;
  border-bottom: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-soft);
}

.mermaid-lightbox__title {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  color: var(--vp-c-text-2);
}

.mermaid-lightbox__btn {
  padding: 4px 12px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 6px;
  background: var(--vp-c-bg);
  color: var(--vp-c-text-1);
  font-size: 13px;
  cursor: pointer;
}

.mermaid-lightbox__btn:hover,
.mermaid-lightbox__btn:focus-visible {
  border-color: var(--vp-c-brand-1);
  outline: none;
}

.mermaid-lightbox__pane {
  height: calc(92vh - 41px);
  padding: 16px;
  overflow: auto;
  box-sizing: border-box;
}

/* Actual size: override mermaid's width:100% / max-width so the SVG
   renders at its viewBox width and the pane scrolls. */
.mermaid-lightbox[data-fit='false'] .mermaid-lightbox__pane svg {
  width: var(--mermaid-natural-width, 100%);
  max-width: none !important;
  height: auto;
}

.mermaid-lightbox[data-fit='true'] .mermaid-lightbox__pane svg {
  width: 100%;
  height: auto;
  max-height: calc(92vh - 73px);
}

@media (prefers-reduced-motion: reduce) {
  .mermaid-figure__surface,
  .mermaid-figure__hint {
    transition: none;
  }
}
</style>
