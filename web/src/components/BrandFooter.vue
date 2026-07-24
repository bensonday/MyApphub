<script setup lang="ts">
import { useProjectBrand } from '../composables/useProjectBrand'

const { projectMark, projectName } = useProjectBrand()
</script>

<template>
  <footer class="brand-footer">
    <span class="footer-mark">{{ projectMark }}</span>
    <span class="footer-label">SECURELY DISTRIBUTED BY <span class="footer-name">{{ projectName }}</span></span>
  </footer>
</template>
