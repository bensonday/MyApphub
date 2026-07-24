import { computed, readonly, ref } from 'vue'
import { api } from '../api/client'

export const DEFAULT_PROJECT_NAME = 'MyApphub'

const currentProjectName = ref(DEFAULT_PROJECT_NAME)
const projectMark = computed(() => Array.from(currentProjectName.value.trim())[0]?.toLocaleUpperCase() ?? 'M')

export function useProjectBrand() {
  return {
    projectName: readonly(currentProjectName),
    projectMark,
  }
}

export async function loadProjectBrand(): Promise<void> {
  try {
    const config = await api.getConfig()
    currentProjectName.value = config.projectName.trim() || DEFAULT_PROJECT_NAME
  } catch {
    currentProjectName.value = DEFAULT_PROJECT_NAME
  }
}
