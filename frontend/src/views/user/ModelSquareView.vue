<template>
  <AppLayout>
    <ModelSquareBackground />

    <div class='relative z-10 mx-auto w-full max-w-[1600px] px-4 py-8 sm:px-6'>
      <ModelSquareHeader
        :search='search'
        :loading='loading'
        @update:search='setSearch'
        @refresh='loadModels'
      />

      <ModelSquarePlatformFilter
        :model-value='platform'
        :platforms='platforms'
        @update:model-value='setPlatform'
      />

      <ModelSquareHint />

      <ModelSquareLoading v-if='loading' />
      <ModelSquareEmpty v-else-if='filteredModels.length === 0' />

      <div v-else class='grid grid-cols-1 lg:grid-cols-[280px_1fr] gap-6 items-start'>
        <ModelSquareModelIndex
          v-model:active-key='activeModelKey'
          :models='filteredModels'
        />
        <div class='space-y-6'>
          <ModelSquareModelCard
            v-for='model in filteredModels'
            :key='model.key'
            :model='model'
            :user-group-rates='userGroupRates'
          />
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang='ts'>
import { ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useModelSquare } from '@/features/model-square/composables/useModelSquare'
import { useModelSquareFilters } from '@/features/model-square/composables/useModelSquareFilters'
import { useModelSquareSearch } from '@/features/model-square/composables/useModelSquareSearch'
import ModelSquareBackground from '@/features/model-square/components/ModelSquareBackground.vue'
import ModelSquareEmpty from '@/features/model-square/components/ModelSquareEmpty.vue'
import ModelSquareHint from '@/features/model-square/components/ModelSquareHint.vue'
import ModelSquareLoading from '@/features/model-square/components/ModelSquareLoading.vue'
import ModelSquarePlatformFilter from '@/features/model-square/components/ModelSquarePlatformFilter.vue'
import ModelSquareHeader from '@/features/model-square/components/v2/ModelSquareHeader.vue'
import ModelSquareModelIndex from '@/features/model-square/components/v2/ModelSquareModelIndex.vue'
import ModelSquareModelCard from '@/features/model-square/components/v2/ModelSquareModelCard.vue'

const { loading, userGroupRates, platforms, modelGroups, loadModels } = useModelSquare()
const { search, debouncedSearch, setSearch } = useModelSquareSearch()
const { platform, setPlatform, filteredModels } = useModelSquareFilters({
  modelGroups,
  search: debouncedSearch,
})

const activeModelKey = ref<string | null>(null)
</script>
