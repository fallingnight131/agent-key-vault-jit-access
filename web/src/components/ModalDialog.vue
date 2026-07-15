<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  title: { type: String, required: true },
  description: { type: String, default: '' },
  eyebrow: { type: String, default: 'AKV CONTROL' },
  submitLabel: { type: String, default: '确认' },
  cancelLabel: { type: String, default: '取消' },
  busy: { type: Boolean, default: false },
  danger: { type: Boolean, default: false },
  wide: { type: Boolean, default: false },
  dismissible: { type: Boolean, default: true },
  showCancel: { type: Boolean, default: true },
  closeOnBackdrop: { type: Boolean, default: false },
  contentKey: { type: String, default: '' },
  error: { type: String, default: '' },
  submitDisabled: { type: Boolean, default: false },
})

const emit = defineEmits(['close', 'submit'])
const dialog = ref(null)
const titleElement = ref(null)
const titleID = useId()
const descriptionID = useId()
let previousFocus = null

function restoreFocus() {
  if (previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus()
  previousFocus = null
}

async function show() {
  const element = dialog.value
  if (!element || element.open) return
  previousFocus = document.activeElement
  if (typeof element.showModal === 'function') element.showModal()
  else element.setAttribute('open', '')
  await nextTick()
  focusInitialControl()
}

function focusInitialControl() {
  const element = dialog.value
  const target = element?.querySelector('[autofocus]:not(:disabled)')
    || element?.querySelector('input:not(:disabled), select:not(:disabled), textarea:not(:disabled), .modal-actions button:not(:disabled)')
    || titleElement.value
  target?.focus()
}

function hide() {
  const element = dialog.value
  if (!element?.open) {
    restoreFocus()
    return
  }
  if (typeof element.close === 'function') element.close()
  else element.removeAttribute('open')
  restoreFocus()
}

function requestClose(event) {
  event?.preventDefault()
  if (props.dismissible && !props.busy) emit('close')
}

function handleBackdrop(event) {
  if (event.target === dialog.value && props.closeOnBackdrop) requestClose(event)
}

function handleSubmit() {
  if (!props.busy && !props.submitDisabled) emit('submit')
}

function handleNativeClose() {
  restoreFocus()
  if (props.open && props.dismissible && !props.busy) emit('close')
}

watch(() => props.open, (open) => {
  if (open) void show()
  else hide()
}, { flush: 'post' })

watch(() => [props.open, props.contentKey], async ([open, key], [wasOpen, previousKey]) => {
  if (!open || !wasOpen || key === previousKey) return
  await nextTick()
  titleElement.value?.focus()
}, { flush: 'post' })

onMounted(() => {
  if (props.open) void show()
})

onBeforeUnmount(hide)
</script>

<template>
  <dialog
    ref="dialog"
    class="modal-dialog"
    :class="{ 'modal-wide': wide }"
    :aria-labelledby="titleID"
    :aria-describedby="description ? descriptionID : undefined"
    @cancel="requestClose"
    @close="handleNativeClose"
    @click="handleBackdrop"
  >
    <form class="modal-shell" novalidate :aria-busy="busy" @submit.prevent="handleSubmit">
      <header class="modal-header">
        <div>
          <p class="eyebrow">{{ eyebrow }}</p>
          <h2 :id="titleID" ref="titleElement" tabindex="-1">{{ title }}</h2>
          <p v-if="description" :id="descriptionID" class="modal-description">{{ description }}</p>
        </div>
        <button
          v-if="dismissible"
          type="button"
          class="modal-close"
          aria-label="关闭弹窗"
          :disabled="busy"
          @click="requestClose"
        >×</button>
      </header>

      <div class="modal-body">
        <slot />
        <p v-if="error" class="modal-error" role="alert">{{ error }}</p>
      </div>

      <footer class="modal-actions">
        <button
          v-if="showCancel"
          type="button"
          class="secondary"
          :disabled="busy"
          @click="requestClose"
        >{{ cancelLabel }}</button>
        <button
          type="submit"
          :class="{ danger }"
          :disabled="busy || submitDisabled"
        >{{ busy ? '处理中…' : submitLabel }}</button>
      </footer>
    </form>
  </dialog>
</template>
