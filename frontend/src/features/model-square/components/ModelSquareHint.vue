<template>
  <div class='mb-8 mx-2 inline-flex items-center gap-2.5 rounded-xl bg-amber-500/5 px-5 py-3 text-xs font-bold text-amber-700 dark:text-amber-300 border border-amber-500/10 shadow-sm'>
    <Icon name='infoCircle' size='xs' />
    <span>提示：下方价格为渠道基础价，实际扣费按所选分组倍率计算；专属倍率和高峰倍率会直接显示在分组标签中。</span>
  </div>
</template>

<script setup lang='ts'>
import Icon from '@/components/icons/Icon.vue'
</script>
