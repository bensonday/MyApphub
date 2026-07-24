import { computed, onBeforeUnmount, ref } from 'vue'

export function useRetryCountdown() {
  const seconds = ref(0)
  let timer: number | undefined

  function stop() {
    window.clearInterval(timer)
    timer = undefined
  }

  function start(value?: number) {
    stop()
    seconds.value = Math.max(1, Math.ceil(value ?? 1))
    timer = window.setInterval(() => {
      seconds.value = Math.max(0, seconds.value - 1)
      if (seconds.value === 0) stop()
    }, 1000)
  }

  onBeforeUnmount(stop)
  return { seconds, active: computed(() => seconds.value > 0), start, stop }
}
