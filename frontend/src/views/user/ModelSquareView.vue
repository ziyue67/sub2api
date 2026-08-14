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

      <div v-else class='grid gap-6 xl:grid-cols-2'>
        <ModelSquareCard
          v-for='model in filteredModels'
          :key='model.key'
          :model='model'
          :user-group-rates='userGroupRates'
        />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang='ts'>
import AppLayout from '@/components/layout/AppLayout.vue'
import { useModelSquare } from '@/features/model-square/composables/useModelSquare'
import { useModelSquareFilters } from '@/features/model-square/composables/useModelSquareFilters'
import { useModelSquareSearch } from '@/features/model-square/composables/useModelSquareSearch'
import ModelSquareBackground from '@/features/model-square/components/ModelSquareBackground.vue'
import ModelSquareCard from '@/features/model-square/components/ModelSquareCard.vue'
import ModelSquareEmpty from '@/features/model-square/components/ModelSquareEmpty.vue'
import ModelSquareHeader from '@/features/model-square/components/ModelSquareHeader.vue'
import ModelSquareHint from '@/features/model-square/components/ModelSquareHint.vue'
import ModelSquareLoading from '@/features/model-square/components/ModelSquareLoading.vue'
import ModelSquarePlatformFilter from '@/features/model-square/components/ModelSquarePlatformFilter.vue'

const { loading, userGroupRates, platforms, modelGroups, loadModels } = useModelSquare()
const { search, setSearch } = useModelSquareSearch()
const { platform, setPlatform, filteredModels } = useModelSquareFilters({
  modelGroups,
  search,
})
</script>
