<template>
  <div class='py-32 text-center'>
    <div class='mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-3xl bg-white/5 border border-white/10'>
      <Icon name='search' size='md' class='text-gray-600' />
    </div>
    <p class='text-sm font-bold text-gray-400 uppercase tracking-widest'>没有可展示的模型</p>
    <p class='mt-2 text-xs text-gray-500'>尝试调整搜索或平台筛选</p>
  </div>
</template>

<script setup lang='ts'>
import Icon from '@/components/icons/Icon.vue'
</script>
