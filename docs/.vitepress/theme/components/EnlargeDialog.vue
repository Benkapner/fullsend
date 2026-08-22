<script setup lang="ts">
import { ref, computed } from 'vue'

// A shared click-to-enlarge dialog for content that the 688px content
// column squeezes: mermaid diagrams (rendered at their viewBox width) and
// wide tables. Callers pass the HTML to show; a "Fit to screen" toggle is
// offered only when a natural width is known (SVG), since tables reflow.
const props = defineProps<{
  title: string
}>()

const dialog = ref<HTMLDialogElement | null>(null)
const open = ref(false)
const fit = ref(false)
const html = ref('')
const naturalWidth = ref<number | null>(null)

const style = computed(() => {
  if (fit.value || !naturalWidth.value) return {}
  return { '--enlarge-natural-width': `${Math.round(naturalWidth.value)}px` }
})

function show(content: string, width: number | null = null) {
  if (!dialog.value) return
  html.value = content
  naturalWidth.value = width
  fit.value = false
  open.value = true
  dialog.value.showModal()
}

function close() {
  open.value = false
  dialog.value?.close()
}

function onBackdropClick(e: MouseEvent) {
  if (e.target === dialog.value) close()
}

defineExpose({ show, close })
</script>

<template>
  <dialog
    ref="dialog"
    class="enlarge-dialog"
    :aria-label="`${props.title}, enlarged`"
    :style="style"
    :data-fit="fit ? 'true' : 'false'"
    @click="onBackdropClick"
    @close="open = false"
  >
    <div class="enlarge-dialog__bar">
      <span class="enlarge-dialog__title">{{ props.title }}</span>
      <button
        v-if="naturalWidth"
        type="button"
        class="enlarge-dialog__btn"
        :aria-pressed="fit"
        @click="fit = !fit"
      >
        {{ fit ? 'Actual size' : 'Fit to screen' }}
      </button>
      <button type="button" class="enlarge-dialog__btn" @click="close">Close</button>
    </div>
    <div v-if="open" class="enlarge-dialog__pane" v-html="html" />
  </dialog>
</template>

<style>
/* Not scoped: the enlarged content arrives through v-html, and the
   figure classes below are applied by other components. Everything is
   namespaced under .enlarge-* / .enlargeable. */

/* --- inline figure affordance, shared by diagrams and tables --- */
.enlargeable {
  position: relative;
}

.enlargeable__hint {
  position: absolute;
  top: 8px;
  right: 8px;
  z-index: 1;
  padding: 2px 8px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: var(--vp-c-bg-soft);
  color: var(--vp-c-text-2);
  font-size: 12px;
  line-height: 18px;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.enlargeable:hover .enlargeable__hint,
.enlargeable:focus-within .enlargeable__hint {
  opacity: 1;
}

@media (hover: none) {
  .enlargeable__hint {
    opacity: 1;
  }
}

/* Tables keep their text selectable; only the pill is the control. */
button.enlargeable__hint {
  cursor: zoom-in;
}

button.enlargeable__hint:hover,
button.enlargeable__hint:focus-visible {
  border-color: var(--vp-c-brand-1);
  color: var(--vp-c-text-1);
  outline: none;
}

.enlargeable--table {
  margin: 16px 0;
}

.enlargeable--table > table {
  margin: 0;
}

/* Code blocks keep VitePress's own top-right copy button (12px from the
   edge, 40px wide) and language label; the pill sits to their left. */
.enlargeable--code > .enlargeable__hint--code {
  top: 12px;
  right: 60px;
  z-index: 3;
}

/* The language label hides on hover in the default theme; keep our pill
   from colliding with it when it is shown. */
.enlargeable--code:hover > span.lang {
  opacity: 0;
}

/* --- the dialog --- */
.enlarge-dialog {
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

.enlarge-dialog::backdrop {
  background: rgb(0 0 0 / 55%);
}

.enlarge-dialog__bar {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 8px 12px;
  border-bottom: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-soft);
}

.enlarge-dialog__title {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  color: var(--vp-c-text-2);
}

.enlarge-dialog__btn {
  padding: 4px 12px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 6px;
  background: var(--vp-c-bg);
  color: var(--vp-c-text-1);
  font-size: 13px;
  cursor: pointer;
}

.enlarge-dialog__btn:hover,
.enlarge-dialog__btn:focus-visible {
  border-color: var(--vp-c-brand-1);
  outline: none;
}

.enlarge-dialog__pane {
  height: calc(92vh - 41px);
  padding: 16px;
  overflow: auto;
  box-sizing: border-box;
}

/* Actual size: override mermaid's width:100% / max-width so the SVG
   renders at its viewBox width and the pane scrolls. */
.enlarge-dialog[data-fit='false'] .enlarge-dialog__pane svg {
  width: var(--enlarge-natural-width, 100%);
  max-width: none !important;
  height: auto;
}

.enlarge-dialog[data-fit='true'] .enlarge-dialog__pane svg {
  width: 100%;
  height: auto;
  max-height: calc(92vh - 73px);
}

/* Tables: use the whole pane and let cells wrap, instead of the
   scroll-box the content column forces on them. */
.enlarge-dialog__pane table {
  display: table;
  width: 100%;
  margin: 0;
  border-collapse: collapse;
  font-size: 14px;
}

.enlarge-dialog__pane th,
.enlarge-dialog__pane td {
  padding: 8px 12px;
  border: 1px solid var(--vp-c-divider);
  vertical-align: top;
}

.enlarge-dialog__pane th {
  background: var(--vp-c-bg-soft);
  text-align: left;
}

/* Code blocks: the clone keeps the theme's highlighting (the dialog lives
   inside .vp-doc); drop the margins and the copied-in controls. */
.enlarge-dialog__pane div[class*='language-'] {
  margin: 0;
}

.enlarge-dialog__pane div[class*='language-'] > button.copy {
  display: none;
}

@media (prefers-reduced-motion: reduce) {
  .enlargeable__hint {
    transition: none;
  }
}
</style>
