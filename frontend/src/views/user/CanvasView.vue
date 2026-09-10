<template>
  <AppLayout>
    <div class="canvas-studio" data-testid="canvas-view">
      <aside class="canvas-panel canvas-list-panel">
        <div class="canvas-panel-header">
          <div class="min-w-0">
            <h1 class="truncate text-lg font-semibold text-gray-900 dark:text-white">{{ t('canvas.title') }}</h1>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('canvas.subtitle') }}</p>
          </div>
          <button
            type="button"
            class="btn btn-primary btn-sm"
            data-testid="canvas-new-button"
            @click="beginNewCanvas"
          >
            <Icon name="plus" size="sm" />
            <span>{{ t('canvas.newCanvas') }}</span>
          </button>
        </div>

        <div class="canvas-section">
          <div class="canvas-section-title">
            <span>{{ t('canvas.myCanvases') }}</span>
            <button
              type="button"
              class="canvas-icon-button"
              :title="t('common.refresh')"
              :disabled="loadingCanvases"
              @click="loadCanvases"
            >
              <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingCanvases }" />
            </button>
          </div>

          <div v-if="canvasLoadError" class="canvas-alert">
            <Icon name="exclamationTriangle" size="sm" />
            <span>{{ canvasLoadError }}</span>
          </div>

          <div class="canvas-list custom-scrollbar" data-testid="canvas-list">
            <div
              v-for="item in canvases"
              :key="item.id"
              class="canvas-list-item"
              :class="{ 'canvas-list-item-active': item.id === selectedCanvasId }"
            >
              <button type="button" class="min-w-0 flex-1 text-left" data-testid="canvas-open-button" @click="openCanvas(item.id)">
                <span class="block truncate text-sm font-semibold">{{ item.name }}</span>
                <span class="mt-1 block truncate text-xs text-gray-500 dark:text-dark-300">
                  {{ canvasMeta(item) }}
                </span>
              </button>
              <button
                type="button"
                class="canvas-icon-button"
                :title="t('canvas.deleteCanvas')"
                :disabled="deletingCanvasIds.has(item.id)"
                data-testid="canvas-delete-button"
                @click.stop="removeCanvas(item)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>

            <div v-if="!loadingCanvases && canvases.length === 0" class="canvas-empty-list">
              <Icon name="inbox" size="lg" />
              <span>{{ t('canvas.emptyList') }}</span>
            </div>
          </div>
        </div>

        <div class="canvas-section">
          <div class="canvas-section-title">
            <span>{{ t('canvas.models') }}</span>
            <button
              type="button"
              class="canvas-icon-button"
              :title="t('common.refresh')"
              :disabled="loadingModels || loadingKeys"
              @click="refreshRunOptions"
            >
              <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingModels || loadingKeys }" />
            </button>
          </div>
          <label class="canvas-field canvas-field-tight">
            <span>{{ t('canvas.apiKey') }}</span>
            <select v-model.number="selectedKeyId" class="input text-sm" data-testid="canvas-api-key-select">
              <option :value="null">{{ t('canvas.selectApiKey') }}</option>
              <option v-for="key in imageApiKeys" :key="key.id" :value="key.id">
                {{ apiKeyLabel(key) }}
              </option>
            </select>
          </label>
          <select v-model="selectedModel" class="input text-sm" data-testid="canvas-model-select">
            <option value="">{{ t('canvas.defaultModel') }}</option>
            <option v-for="modelItem in models" :key="modelItem.id" :value="modelItem.id">
              {{ modelLabel(modelItem) }}
            </option>
          </select>
          <p v-if="!loadingKeys && apiKeys.length === 0" class="mt-2 text-xs leading-5 text-rose-600 dark:text-rose-300">
            {{ t('canvas.noUsableApiKey') }}
          </p>
          <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-300">{{ t('canvas.modelHint') }}</p>
        </div>
      </aside>

      <main class="canvas-workspace">
        <header class="canvas-toolbar">
          <div class="min-w-0 flex-1">
            <input
              v-model="draftName"
              type="text"
              class="canvas-title-input"
              :placeholder="t('canvas.namePlaceholder')"
              data-testid="canvas-name-input"
            />
            <input
              v-model="draftDescription"
              type="text"
              class="canvas-description-input"
              :placeholder="t('canvas.descriptionPlaceholder')"
            />
          </div>
          <div class="canvas-toolbar-actions">
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="!hasActiveCanvasImageTasks || cancelingActiveTasks"
              data-testid="canvas-cancel-active-tasks-button"
              @click="cancelActiveCanvasTasks"
            >
              <Icon name="ban" size="sm" />
              <span>{{ t('canvas.cancelRun') }}</span>
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="!canQueueRun"
              data-testid="canvas-run-button"
              @click="queueCanvasRun"
            >
              <Icon name="play" size="sm" :class="{ 'animate-pulse': queuingRun }" />
              <span>{{ queuingRun ? t('canvas.queuing') : t('canvas.queueRun') }}</span>
            </button>
            <button
              type="button"
              class="btn btn-primary btn-sm"
              :disabled="!canSave"
              data-testid="canvas-save-button"
              @click="saveCanvas"
            >
              <Icon name="check" size="sm" />
              <span>{{ saving ? t('canvas.saving') : t('canvas.saveCanvas') }}</span>
            </button>
          </div>
        </header>

        <div v-if="latestRun" class="canvas-latest-run" data-testid="canvas-latest-run">
          <span class="canvas-run-status" :class="`canvas-run-status-${latestRun.status}`"></span>
          <span class="min-w-0 flex-1 truncate">
            {{ t('canvas.latestRun') }} · {{ runStatusLabel(latestRun.status) }} · {{ formatDate(latestRun.updated_at) }}
          </span>
          <span v-if="runOutputSummary(latestRun)" class="truncate">{{ runOutputSummary(latestRun) }}</span>
          <span v-if="latestRun.error_message" class="truncate text-rose-600 dark:text-rose-300">
            {{ latestRun.error_message }}
          </span>
        </div>

        <section class="canvas-stage-shell">
          <div class="canvas-stage-header">
            <div class="canvas-stage-title">
              <span>{{ t('canvas.stage') }}</span>
              <span>{{ t('canvas.nodeCount', { count: canvasDocument.nodes.length }) }}</span>
              <span>{{ t('canvas.edgeCount', { count: canvasDocument.edges.length }) }}</span>
            </div>
            <div class="canvas-stage-tools">
              <button
                type="button"
                class="canvas-icon-button"
                :title="t('canvas.zoomOut')"
                data-testid="canvas-zoom-out-button"
                @click="zoomCanvasBy(0.9)"
              >
                <Icon name="zoomOut" size="sm" />
              </button>
              <span class="canvas-zoom-value" data-testid="canvas-zoom-value">{{ viewportZoomLabel }}</span>
              <button
                type="button"
                class="canvas-icon-button"
                :title="t('canvas.zoomIn')"
                data-testid="canvas-zoom-in-button"
                @click="zoomCanvasBy(1.1)"
              >
                <Icon name="zoomIn" size="sm" />
              </button>
              <button
                type="button"
                class="canvas-icon-button"
                :title="t('canvas.fitView')"
                data-testid="canvas-fit-view-button"
                @click="fitCanvasView"
              >
                <Icon name="grid" size="sm" />
              </button>
            </div>
          </div>
          <div
            ref="stageRef"
            class="canvas-stage custom-scrollbar"
            :class="{ 'canvas-stage-panning': canvasPanState !== null }"
            :style="stageGridStyle"
            data-testid="canvas-stage"
            @mousedown="startCanvasPan"
            @wheel.prevent="handleCanvasWheel"
          >
            <div class="canvas-stage-content" :style="stageContentStyle">
              <svg
                class="canvas-edges"
                :viewBox="`0 0 ${canvasWorldSize.width} ${canvasWorldSize.height}`"
                preserveAspectRatio="none"
                aria-hidden="true"
              >
                <path
                  v-for="edge in edgeLines"
                  :key="edge.id"
                  :d="edge.path"
                  class="canvas-edge"
                  :class="{ 'canvas-edge-selected': edge.id === selectedEdgeId }"
                  data-testid="canvas-edge"
                  @mousedown.stop
                  @click.stop="selectEdge(edge.id)"
                />
              </svg>
              <button
                v-for="node in canvasDocument.nodes"
                :key="node.id"
                type="button"
                class="canvas-node"
                :class="[
                  nodeKindClass(node.type),
                  {
                    'canvas-node-selected': node.id === selectedNodeId,
                    'canvas-node-link-source': node.id === linkSourceNodeId,
                  }
                ]"
                :style="nodeStyle(node)"
                data-testid="canvas-node"
                @mousedown.stop="startNodeDrag(node, $event)"
                @click.stop="selectOrConnectNode(node.id)"
              >
                <span class="canvas-node-kind">{{ nodeTypeLabel(node.type) }}</span>
                <span class="canvas-node-title">{{ node.title }}</span>
                <span class="canvas-node-status">
                  <span class="canvas-node-status-dot" :class="`canvas-node-status-${nodeDisplayStatus(node)}`"></span>
                  {{ t(`canvas.nodeStatus.${nodeDisplayStatus(node)}`) }}
                </span>
                <span v-if="node.type === 'result' && nodeResultPreviewUrl(node)" class="canvas-node-preview">
                  <img
                    :src="nodeResultPreviewUrl(node)"
                    :alt="t('canvas.resultPreview')"
                    :title="t('canvas.openImagePreview')"
                    data-testid="canvas-node-preview-image"
                    draggable="false"
                    @dblclick.stop.prevent="openImagePreview(node)"
                  />
                </span>
                <span
                  v-else-if="nodeResultSummary(node)"
                  class="canvas-node-result-summary"
                  data-testid="canvas-node-result-summary"
                >
                  {{ nodeResultSummary(node) }}
                </span>
                <span v-if="nodeErrorSummary(node)" class="canvas-node-error" data-testid="canvas-node-error">
                  {{ nodeErrorSummary(node) }}
                </span>
              </button>
            </div>

            <div v-if="canvasDocument.nodes.length === 0" class="canvas-stage-empty">
              <Icon name="cube" size="xl" />
              <span>{{ t('canvas.emptyStage') }}</span>
            </div>
          </div>
        </section>
      </main>

      <aside class="canvas-panel canvas-inspector-panel">
        <div class="canvas-section">
          <div class="canvas-section-title">
            <span>{{ t('canvas.nodeTypesLabel') }}</span>
          </div>
          <div class="canvas-node-type-grid">
            <button
              v-for="item in nodeTypeItems"
              :key="item.type"
              type="button"
              class="canvas-node-type-button"
              :class="nodeKindClass(item.type)"
              :data-testid="`canvas-node-type-${item.type}`"
              @click="addNode(item.type)"
            >
              <Icon :name="item.icon" size="sm" />
              <span>{{ item.label }}</span>
            </button>
          </div>
        </div>

        <div class="canvas-section">
          <div class="canvas-section-title">
            <span>{{ t('canvas.nodeList') }}</span>
            <span class="canvas-section-actions">
              <button
                type="button"
                class="canvas-icon-button"
                :class="{ 'canvas-icon-button-active': linkSourceNodeId }"
                :title="linkSourceNodeId ? t('canvas.cancelLink') : t('canvas.createEdge')"
                :disabled="!selectedNode"
                data-testid="canvas-create-edge-button"
                @click="toggleEdgeCreation"
              >
                <Icon name="link" size="sm" />
              </button>
              <button
                type="button"
                class="canvas-icon-button"
                :title="t('canvas.removeEdge')"
                :disabled="!selectedEdge"
                data-testid="canvas-remove-edge-button"
                @click="removeSelectedEdge"
              >
                <Icon name="x" size="sm" />
              </button>
              <button
                type="button"
                class="canvas-icon-button"
                :title="t('canvas.removeNode')"
                :disabled="!selectedNode"
                data-testid="canvas-remove-node-button"
                @click="removeSelectedNode"
              >
                <Icon name="trash" size="sm" />
              </button>
            </span>
          </div>
          <div class="canvas-node-list custom-scrollbar" data-testid="canvas-node-list">
            <button
              v-for="node in canvasDocument.nodes"
              :key="node.id"
              type="button"
              class="canvas-node-list-item"
              :class="{ 'canvas-node-list-item-active': node.id === selectedNodeId }"
              @click="selectedNodeId = node.id"
            >
              <span class="canvas-node-list-dot" :class="nodeKindClass(node.type)"></span>
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium">{{ node.title }}</span>
                <span class="block truncate text-xs text-gray-500 dark:text-dark-300">{{ nodeTypeLabel(node.type) }}</span>
              </span>
            </button>
          </div>
        </div>

        <div class="canvas-section">
          <div class="canvas-section-title">
            <span>{{ t('canvas.nodeInspector') }}</span>
          </div>
          <div v-if="selectedNode" class="canvas-node-editor" data-testid="canvas-node-editor">
            <label class="canvas-field">
              <span>{{ t('canvas.nodeTitle') }}</span>
              <input
                :value="selectedNode.title"
                type="text"
                class="input text-sm"
                data-testid="canvas-node-title-input"
                @input="updateSelectedNodeTitleFromEvent"
              />
            </label>

            <div class="canvas-node-editor-status">
              <span class="canvas-run-status" :class="`canvas-run-status-${nodeDisplayStatus(selectedNode)}`"></span>
              <span>{{ t(`canvas.nodeStatus.${nodeDisplayStatus(selectedNode)}`) }}</span>
              <button
                v-if="selectedNode.type === 'result'"
                type="button"
                class="canvas-node-download-button"
                :title="t('canvas.downloadImage')"
                :disabled="!nodeResultImageUrl(selectedNode) || downloadingImage"
                data-testid="canvas-node-download-image"
                @click="downloadNodeImage(selectedNode)"
              >
                <Icon name="download" size="sm" />
                <span>{{ downloadingImage ? t('canvas.downloadingImage') : t('canvas.downloadImage') }}</span>
              </button>
            </div>

            <div v-if="selectedNode.type === 'result'" class="canvas-result-output" data-testid="canvas-result-output">
              <template v-if="nodeResultPreviewUrl(selectedNode)">
                <img
                  :src="nodeResultPreviewUrl(selectedNode)"
                  :alt="t('canvas.resultPreview')"
                  :title="t('canvas.openImagePreview')"
                  class="canvas-result-output-image"
                  data-testid="canvas-result-output-image"
                  draggable="false"
                  @dblclick.stop.prevent="openImagePreview(selectedNode)"
                />
                <p>{{ t('canvas.resultImageReady') }}</p>
              </template>
              <template v-else-if="nodeResultImageUrl(selectedNode)">
                <Icon name="refresh" size="md" class="animate-spin" />
                <p>{{ t('canvas.resultImageLoading') }}</p>
              </template>
              <template v-else>
                <Icon name="image" size="md" />
                <p>{{ t('canvas.resultImagePending') }}</p>
              </template>
            </div>

            <datalist id="canvas-model-options">
              <option v-for="modelItem in selectedNodeModels" :key="modelItem.id" :value="modelItem.id">
                {{ modelLabel(modelItem) }}
              </option>
            </datalist>

            <label v-if="selectedNodeSupportsApiKey" class="canvas-field">
              <span>{{ t('canvas.nodeConfig.apiKey') }}</span>
              <select
                :value="selectedNodeApiKeyId || ''"
                class="input text-sm"
                data-testid="canvas-node-api-key-select"
                @change="setSelectedNodeApiKeyFromEvent"
              >
                <option value="">{{ t('canvas.nodeConfig.useCanvasApiKey') }}</option>
                <option v-for="key in apiKeys" :key="key.id" :value="key.id">
                  {{ apiKeyLabel(key) }}
                </option>
              </select>
            </label>

            <label
              v-for="field in selectedNodeBasicConfigFields"
              :key="field.key"
              class="canvas-field"
            >
              <span>{{ t(field.labelKey) }}</span>
              <textarea
                v-if="field.kind === 'textarea'"
                :value="selectedNodeConfigValue(field.key)"
                class="input canvas-textarea"
                rows="3"
                :placeholder="t(field.placeholderKey)"
                :data-testid="`canvas-node-config-${field.key}`"
                @input="updateSelectedNodeConfigFromEvent(field.key, $event)"
              ></textarea>
              <select
                v-else-if="field.kind === 'select'"
                :value="selectedNodeConfigValue(field.key)"
                class="input text-sm"
                :data-testid="`canvas-node-config-${field.key}`"
                @change="updateSelectedNodeConfigFromEvent(field.key, $event)"
              >
                <option value="">{{ field.key === 'outputFormat' ? t('canvas.nodeConfigDefaultWebp') : t('canvas.nodeConfigDefault') }}</option>
                <option v-for="option in field.options" :key="option.value" :value="option.value">
                  {{ t(option.labelKey) }}
                </option>
              </select>
              <input
                v-else
                :value="selectedNodeConfigValue(field.key)"
                type="text"
                class="input text-sm"
                :list="field.key === 'model' ? 'canvas-model-options' : undefined"
                :placeholder="t(field.placeholderKey)"
                :data-testid="`canvas-node-config-${field.key}`"
                @input="updateSelectedNodeConfigFromEvent(field.key, $event)"
              />
            </label>

            <div v-if="selectedNode.type === 'image' || selectedNode.type === 'image_to_image'" class="canvas-field">
              <span>{{ t('canvas.nodeConfig.referenceImage') }}</span>
              <label class="canvas-reference-upload" :class="{ 'canvas-reference-upload-busy': uploadingReferenceImage }">
                <input
                  type="file"
                  class="sr-only"
                  accept="image/png,image/jpeg,image/webp"
                  :disabled="uploadingReferenceImage || !selectedNodeApiKey"
                  data-testid="canvas-reference-image-upload"
                  @change="uploadSelectedNodeReferenceImage"
                />
                <img v-if="selectedNodeReferencePreviewUrl" :src="selectedNodeReferencePreviewUrl" alt="" />
                <Icon v-else name="upload" size="md" />
                <span>{{ uploadingReferenceImage ? t('canvas.uploadingReferenceImage') : t('canvas.uploadReferenceImage') }}</span>
                <small v-if="selectedNode.config?.referenceImageName">{{ String(selectedNode.config.referenceImageName) }}</small>
              </label>
            </div>

            <template v-if="selectedNodeSupportsImageDimensions">
              <div class="canvas-field">
                <span>{{ t('canvas.nodeConfig.dimensionMode') }}</span>
                <div class="canvas-dimension-mode" role="group" :aria-label="t('canvas.nodeConfig.dimensionMode')">
                  <button
                    v-for="option in canvasDimensionModeOptions"
                    :key="option.value"
                    type="button"
                    class="canvas-dimension-mode-button"
                    :class="{ 'canvas-dimension-mode-button-active': selectedNodeDimensionMode === option.value }"
                    :aria-pressed="selectedNodeDimensionMode === option.value"
                    @click="setSelectedNodeDimensionMode(option.value)"
                  >
                    {{ t(option.labelKey) }}
                  </button>
                </div>
              </div>

              <template v-if="selectedNodeDimensionMode === 'ratio'">
                <div class="canvas-field">
                  <span>{{ t('canvas.nodeConfig.resolution') }}</span>
                  <div class="canvas-resolution-grid" role="group" :aria-label="t('canvas.nodeConfig.resolution')">
                    <button
                      v-for="option in canvasResolutionOptions"
                      :key="option.value"
                      type="button"
                      class="canvas-resolution-button"
                      :class="{ 'canvas-resolution-button-active': selectedNodeResolution === option.value }"
                      :aria-pressed="selectedNodeResolution === option.value"
                      @click="setSelectedNodeResolution(option.value)"
                    >
                      {{ option.label }}
                    </button>
                  </div>
                </div>
                <div class="canvas-field">
                  <span>{{ t('canvas.nodeConfig.aspectRatio') }}</span>
                  <div class="canvas-aspect-grid" role="group" :aria-label="t('canvas.nodeConfig.aspectRatio')">
                    <button
                      v-for="option in canvasAspectRatioOptions"
                      :key="option.value"
                      type="button"
                      class="canvas-aspect-button"
                      :class="{ 'canvas-aspect-button-active': selectedNodeAspectRatio === option.value }"
                      :aria-pressed="selectedNodeAspectRatio === option.value"
                      @click="setSelectedNodeAspectRatio(option.value)"
                    >
                      <span class="canvas-aspect-shape" :style="canvasAspectRatioShapeStyle(option.value)"></span>
                      <span>{{ option.label }}</span>
                    </button>
                  </div>
                </div>
                <p class="canvas-dimension-summary">{{ selectedNodeResolvedSize }}</p>
              </template>

              <template v-else-if="selectedNodeDimensionMode === 'custom'">
                <div class="canvas-custom-dimension-grid">
                  <label class="canvas-field">
                    <span>{{ t('canvas.nodeConfig.width') }}</span>
                    <input
                      :value="selectedNodeCustomWidth"
                      class="input text-sm"
                      type="number"
                      min="16"
                      max="3840"
                      step="16"
                      inputmode="numeric"
                      data-testid="canvas-node-config-width"
                      @input="setSelectedNodeCustomDimension('width', $event)"
                    />
                  </label>
                  <span class="canvas-dimension-cross" aria-hidden="true">x</span>
                  <label class="canvas-field">
                    <span>{{ t('canvas.nodeConfig.height') }}</span>
                    <input
                      :value="selectedNodeCustomHeight"
                      class="input text-sm"
                      type="number"
                      min="16"
                      max="3840"
                      step="16"
                      inputmode="numeric"
                      data-testid="canvas-node-config-height"
                      @input="setSelectedNodeCustomDimension('height', $event)"
                    />
                  </label>
                </div>
                <p class="canvas-dimension-summary" :class="{ 'canvas-dimension-summary-error': selectedNodeDimensionError }">
                  {{ selectedNodeDimensionError || selectedNodeResolvedSize }}
                </p>
              </template>

              <p v-else class="canvas-dimension-summary">{{ t('canvas.nodeConfig.autoSizeHint') }}</p>
            </template>

            <div v-if="selectedNodeBasicConfigFields.length === 0 && !selectedNodeSupportsImageDimensions && selectedNode.type !== 'result'" class="canvas-placeholder canvas-compact-placeholder">
              <span>{{ t('canvas.noConfigFields') }}</span>
            </div>
          </div>
          <div v-else class="canvas-placeholder canvas-compact-placeholder">
            <span>{{ t('canvas.selectedNodePlaceholder') }}</span>
          </div>
        </div>

        <div class="canvas-section">
          <div class="canvas-section-title">
            <span>{{ t('canvas.runHistory') }}</span>
          </div>
          <div class="canvas-run-list custom-scrollbar" data-testid="canvas-run-list">
            <div v-for="run in runs" :key="run.id" class="canvas-run-item">
              <span class="canvas-run-status" :class="`canvas-run-status-${run.status}`"></span>
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium">{{ runStatusLabel(run.status) }}</span>
                <span class="block truncate text-xs text-gray-500 dark:text-dark-300">{{ formatDate(run.created_at) }}</span>
                <span v-if="runOutputSummary(run)" class="block truncate text-xs text-emerald-600 dark:text-emerald-300">
                  {{ runOutputSummary(run) }}
                </span>
                <span v-if="run.error_message" class="block truncate text-xs text-rose-600 dark:text-rose-300">
                  {{ run.error_message }}
                </span>
              </span>
              <button
                v-if="canCancelRun(run)"
                type="button"
                class="canvas-icon-button"
                :title="t('canvas.cancelRun')"
                :disabled="cancelingRunIds.has(run.id)"
                data-testid="canvas-cancel-run-button"
                @click="cancelRun(run)"
              >
                <Icon name="ban" size="sm" />
              </button>
            </div>
            <div v-if="runs.length === 0" class="canvas-placeholder">
              <Icon name="clock" size="md" />
              <span>{{ t('canvas.runPlaceholder') }}</span>
            </div>
          </div>
        </div>

        <div class="canvas-section">
          <div class="canvas-section-title">
            <span>{{ t('canvas.templates') }}</span>
          </div>
          <div class="canvas-template-entry">
            <Icon name="book" size="md" />
            <div class="min-w-0">
              <p class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ t('canvas.templateEntry') }}</p>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-300">{{ t('canvas.templatePlaceholder') }}</p>
            </div>
          </div>
        </div>
      </aside>
    </div>
  </AppLayout>

  <Teleport to="body">
    <div
      v-if="previewImageUrl"
      class="canvas-image-preview-overlay"
      data-testid="canvas-image-preview-overlay"
      @click.self="closeImagePreview"
    >
      <section class="canvas-image-preview" role="dialog" aria-modal="true" :aria-label="t('canvas.imagePreview')">
        <header class="canvas-image-preview-header">
          <span class="truncate">{{ previewImageName }}</span>
          <div class="canvas-image-preview-actions">
            <button type="button" class="canvas-icon-button" :title="t('canvas.downloadImage')" @click="downloadPreviewImage">
              <Icon name="download" size="sm" />
            </button>
            <button type="button" class="canvas-icon-button" :title="t('canvas.closeImagePreview')" @click="closeImagePreview">
              <Icon name="x" size="sm" />
            </button>
          </div>
        </header>
        <img :src="previewImageUrl" :alt="t('canvas.imagePreview')" data-testid="canvas-image-preview-image" />
      </section>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { keysAPI } from '@/api/keys'
import {
  cancelCanvasRun,
  createCanvas,
  createCanvasRun,
  deleteCanvas,
  getCanvas,
  listCanvasModels,
  listCanvasRuns,
  listCanvases,
  updateCanvas,
  type CanvasDocument,
  type CanvasModel,
  type CanvasNode,
  type CanvasNodeType,
  type CanvasRun,
  type CanvasRunStatus,
  type UserCanvas,
  type UserCanvasSummary,
} from '@/api/canvas'
import {
  downloadImageFile,
  getImageTask,
  uploadImageCreatorReference,
  type ImageCreatorTask,
  type ImageCreatorTaskStatus,
} from '@/api/imageCreator'
import type { ApiKey } from '@/types'
import { apiKeySupportsOpenAIImageGeneration, primaryAPIKeyImageGroupName } from '@/utils/apiKeyCapabilities'
import { displayModelLabel } from '@/utils/modelDisplay'

type IconName = InstanceType<typeof Icon>['$props']['name']

interface NodeTypeItem {
  type: CanvasNodeType
  label: string
  icon: IconName
}

interface EdgeLine {
  id: string
  path: string
}

interface CanvasViewport {
  x: number
  y: number
  zoom: number
}

interface CanvasDragState {
  nodeId: string
  startClientX: number
  startClientY: number
  startNodeX: number
  startNodeY: number
}

interface CanvasPanState {
  startClientX: number
  startClientY: number
  startViewportX: number
  startViewportY: number
}

type CanvasNodeStatus = NonNullable<CanvasNode['status']>
type NodeConfigKey = 'prompt' | 'text' | 'model' | 'size' | 'quality' | 'referenceImageName' | 'outputFormat'
type NodeConfigFieldKind = 'input' | 'textarea' | 'select'
type CanvasDimensionMode = 'auto' | 'ratio' | 'custom'

interface NodeConfigOption {
  value: string
  labelKey: string
}

interface NodeConfigField {
  key: NodeConfigKey
  kind: NodeConfigFieldKind
  labelKey: string
  placeholderKey: string
  options: NodeConfigOption[]
}

interface CanvasRunImageTaskLink {
  nodeId: string
  taskId: number
  taskStatus?: ImageCreatorTaskStatus
}

const { t } = useI18n()
const appStore = useAppStore()

const canvases = ref<UserCanvasSummary[]>([])
const models = ref<CanvasModel[]>([])
const selectedNodeModels = ref<CanvasModel[]>([])
const runs = ref<CanvasRun[]>([])
const apiKeys = ref<ApiKey[]>([])
const canvasTaskLinks = ref<CanvasRunImageTaskLink[]>([])
const canvasTasksById = ref<Record<string, ImageCreatorTask>>({})
const previewImageUrl = ref('')
const previewImageName = ref('')
const previewImageSourceUrl = ref('')
const imagePreviewUrls = ref<Record<string, string>>({})
const uploadingReferenceImage = ref(false)
const downloadingImage = ref(false)
const stageRef = ref<HTMLElement | null>(null)
const selectedCanvasId = ref<string | null>(null)
const selectedNodeId = ref<string | null>(null)
const selectedEdgeId = ref<string | null>(null)
const linkSourceNodeId = ref<string | null>(null)
const cancelingRunIds = ref(new Set<string>())
const deletingCanvasIds = ref(new Set<string>())
const cancelingActiveTasks = ref(false)
const selectedKeyId = ref<number | null>(null)
const draftName = ref('')
const draftDescription = ref('')
const selectedModel = ref('')
const canvasDocument = ref<CanvasDocument>(createDefaultDocument())
const loadingCanvases = ref(false)
const loadingCanvas = ref(false)
const loadingKeys = ref(false)
const loadingModels = ref(false)
const saving = ref(false)
const queuingRun = ref(false)
const canvasLoadError = ref('')
const canvasTaskPollIntervalMs = 4000
let canvasTaskPollTimerId: ReturnType<typeof setInterval> | null = null
let pollingCanvasTasks = false
let canvasTaskSyncVersion = 0
const imagePreviewLoads = new Map<string, Promise<string>>()
const canvasDragState = ref<CanvasDragState | null>(null)
const canvasPanState = ref<CanvasPanState | null>(null)
let canvasPointerListenersActive = false
let modelsRequestId = 0
let selectedNodeModelsRequestId = 0

const canvasWorldSize = {
  width: 1400,
  height: 900,
}

const canvasViewportDefaults: CanvasViewport = {
  x: 0,
  y: 0,
  zoom: 1,
}

const nodeTypes: Array<{ type: CanvasNodeType, icon: IconName }> = [
  { type: 'text', icon: 'document' },
  { type: 'image', icon: 'image' },
  { type: 'prompt', icon: 'chatBubble' },
  { type: 'loop', icon: 'sync' },
  { type: 'group', icon: 'folder' },
  { type: 'text_to_image', icon: 'sparkles' },
  { type: 'image_to_image', icon: 'swap' },
  { type: 'result', icon: 'checkCircle' },
]

const qualityOptions: NodeConfigOption[] = [
  { value: 'auto', labelKey: 'canvas.nodeConfigOptions.quality.auto' },
  { value: 'low', labelKey: 'canvas.nodeConfigOptions.quality.low' },
  { value: 'medium', labelKey: 'canvas.nodeConfigOptions.quality.medium' },
  { value: 'high', labelKey: 'canvas.nodeConfigOptions.quality.high' },
]

const outputFormatOptions = [
  { value: 'png', labelKey: 'canvas.nodeConfigOptions.outputFormat.png' },
  { value: 'jpeg', labelKey: 'canvas.nodeConfigOptions.outputFormat.jpeg' },
  { value: 'webp', labelKey: 'canvas.nodeConfigOptions.outputFormat.webp' },
]

const nodeConfigFields: Record<CanvasNodeType, NodeConfigField[]> = {
  prompt: [
    makeConfigField('prompt', 'textarea'),
    makeConfigField('model', 'input'),
  ],
  text: [
    makeConfigField('text', 'textarea'),
    makeConfigField('model', 'input'),
  ],
  image: [],
  text_to_image: [
    makeConfigField('prompt', 'textarea'),
    makeConfigField('model', 'input'),
    makeConfigField('quality', 'select', qualityOptions),
    makeConfigField('outputFormat', 'select', outputFormatOptions),
  ],
  image_to_image: [
    makeConfigField('prompt', 'textarea'),
    makeConfigField('model', 'input'),
    makeConfigField('quality', 'select', qualityOptions),
    makeConfigField('outputFormat', 'select', outputFormatOptions),
  ],
  loop: [
    makeConfigField('text', 'input'),
  ],
  group: [],
  result: [],
}

const nodeTypeItems = computed<NodeTypeItem[]>(() =>
  nodeTypes.map((item) => ({
    ...item,
    label: nodeTypeLabel(item.type),
  }))
)

const selectedNode = computed(() =>
  canvasDocument.value.nodes.find((node) => node.id === selectedNodeId.value) ?? null
)

const selectedEdge = computed(() =>
  canvasDocument.value.edges.find((edge) => edge.id === selectedEdgeId.value) ?? null
)

const selectedNodeConfigFields = computed(() =>
  selectedNode.value ? nodeConfigFields[selectedNode.value.type] : []
)

const selectedNodeBasicConfigFields = computed(() =>
  selectedNodeConfigFields.value.filter((field) => field.key !== 'size')
)

const selectedNodeSupportsImageDimensions = computed(() =>
  selectedNode.value?.type === 'text_to_image' || selectedNode.value?.type === 'image_to_image'
)

const selectedNodeSupportsApiKey = computed(() =>
  selectedNode.value?.type === 'text' ||
  selectedNode.value?.type === 'prompt' ||
  selectedNode.value?.type === 'text_to_image' ||
  selectedNode.value?.type === 'image_to_image'
)

const selectedNodeApiKeyId = computed(() => {
  const value = positiveIntegerFromUnknown(selectedNode.value?.config?.apiKeyId)
  return value ?? selectedKeyId.value
})

const selectedNodeApiKey = computed(() =>
  apiKeys.value.find((key) => key.id === selectedNodeApiKeyId.value) ?? null
)

const selectedNodeReferencePreviewUrl = computed(() => {
  const url = typeof selectedNode.value?.config?.referenceImageUrl === 'string' ? selectedNode.value.config.referenceImageUrl : ''
  return url ? imagePreviewUrls.value[url] || '' : ''
})

const canvasDimensionModeOptions: NodeConfigOption[] = [
  { value: 'auto', labelKey: 'canvas.nodeConfig.dimensionModes.auto' },
  { value: 'ratio', labelKey: 'canvas.nodeConfig.dimensionModes.ratio' },
  { value: 'custom', labelKey: 'canvas.nodeConfig.dimensionModes.custom' },
]

const canvasResolutionOptions = [
  { value: '1K', label: '1K' },
  { value: '2K', label: '2K' },
  { value: '4K', label: '4K' },
]

const canvasAspectRatioOptions = [
  { value: '1:1', label: '1:1' },
  { value: '3:2', label: '3:2' },
  { value: '2:3', label: '2:3' },
  { value: '16:9', label: '16:9' },
  { value: '9:16', label: '9:16' },
  { value: '4:3', label: '4:3' },
  { value: '3:4', label: '3:4' },
  { value: '21:9', label: '21:9' },
]

const selectedNodeDimensionMode = computed<CanvasDimensionMode>(() => {
  const value = selectedNode.value?.config?.dimensionMode
  return value === 'auto' || value === 'custom' || value === 'ratio' ? value : 'ratio'
})

const selectedNodeResolution = computed(() => {
  const value = selectedNode.value?.config?.resolution
  return value === '2K' || value === '4K' ? value : '1K'
})

const selectedNodeAspectRatio = computed(() => {
  const value = selectedNode.value?.config?.aspectRatio
  return canvasAspectRatioOptions.some((option) => option.value === value) ? String(value) : '1:1'
})

const selectedNodeCustomWidth = computed(() => canvasNodePositiveIntegerConfig('width', 1024))
const selectedNodeCustomHeight = computed(() => canvasNodePositiveIntegerConfig('height', 1024))

const selectedNodeDimensionError = computed(() => {
  if (!selectedNodeSupportsImageDimensions.value || selectedNodeDimensionMode.value !== 'custom') return ''
  return canvasDimensionValidationError(selectedNodeCustomWidth.value, selectedNodeCustomHeight.value)
})

const selectedNodeResolvedSize = computed(() => {
  if (selectedNodeDimensionMode.value === 'auto') return 'auto'
  if (selectedNodeDimensionMode.value === 'custom') return `${selectedNodeCustomWidth.value}x${selectedNodeCustomHeight.value}`
  return canvasSizeForResolutionAndRatio(selectedNodeResolution.value, selectedNodeAspectRatio.value)
})

const latestRun = computed(() => runs.value[0] ?? null)
const activeCanvasRun = computed(() => runs.value.find((run) =>
  canvasImageTaskLinksFromRun(run).some((link) => {
    const status = canvasTasksById.value[String(link.taskId)]?.status ?? link.taskStatus
    return !status || taskIsActiveStatus(status)
  })
) ?? null)
const hasActiveCanvasImageTasks = computed(() => activeCanvasRun.value !== null)

const selectedKey = computed(() =>
  imageApiKeys.value.find((key) => key.id === selectedKeyId.value) ?? null
)

const imageApiKeys = computed(() => apiKeys.value.filter(apiKeySupportsOpenAIImageGeneration))

const canSave = computed(() =>
  !saving.value &&
  !loadingCanvas.value &&
  draftName.value.trim().length > 0 &&
  canvasDocument.value.nodes.length > 0
)

const canQueueRun = computed(() =>
  !queuingRun.value &&
  !saving.value &&
  !loadingCanvas.value &&
  draftName.value.trim().length > 0 &&
  canvasDocument.value.nodes.length > 0 &&
  !canvasDocument.value.nodes.some((node) => canvasNodeHasInvalidCustomDimensions(node))
)

const viewportZoomLabel = computed(() => `${Math.round(currentViewport().zoom * 100)}%`)

const stageContentStyle = computed<Record<string, string>>(() => {
  const viewport = currentViewport()
  return {
    height: `${canvasWorldSize.height}px`,
    width: `${canvasWorldSize.width}px`,
    transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})`,
  }
})

const stageGridStyle = computed<Record<string, string>>(() => {
  const viewport = currentViewport()
  return {
    backgroundPosition: `${viewport.x}px ${viewport.y}px`,
    backgroundSize: `${28 * viewport.zoom}px ${28 * viewport.zoom}px`,
  }
})

const edgeLines = computed<EdgeLine[]>(() => {
  const nodeById = new Map(canvasDocument.value.nodes.map((node) => [node.id, node]))
  return canvasDocument.value.edges
    .map((edge) => {
      const source = nodeById.get(edge.source_node_id)
      const target = nodeById.get(edge.target_node_id)
      if (!source || !target) return null
      const sx = source.x + (source.width || 160)
      const sy = source.y + ((source.height || 82) / 2)
      const tx = target.x
      const ty = target.y + ((target.height || 82) / 2)
      const mid = Math.max(40, Math.abs(tx - sx) / 2)
      return {
        id: edge.id,
        path: `M ${sx} ${sy} C ${sx + mid} ${sy}, ${tx - mid} ${ty}, ${tx} ${ty}`,
      }
    })
    .filter((line): line is EdgeLine => line !== null)
})

onMounted(() => {
  void loadCanvases()
  void loadApiKeys()
  void loadModels()
})

onBeforeUnmount(() => {
  removeCanvasPointerListeners()
  stopCanvasTaskPolling()
  clearCanvasImagePreviews()
})

function createDefaultDocument(): CanvasDocument {
  const nodes: CanvasNode[] = [
    makeNode('prompt', 'canvas.sampleNodes.prompt', 70, 70),
    makeNode('text', 'canvas.sampleNodes.text', 290, 70),
    makeNode('text_to_image', 'canvas.sampleNodes.textToImage', 510, 70),
    makeNode('image', 'canvas.sampleNodes.image', 730, 70),
    makeNode('image_to_image', 'canvas.sampleNodes.imageToImage', 180, 250),
    makeNode('loop', 'canvas.sampleNodes.loop', 400, 250),
    makeNode('group', 'canvas.sampleNodes.group', 620, 250),
    makeNode('result', 'canvas.sampleNodes.result', 400, 430),
  ]
  return {
    nodes,
    edges: [
      makeEdge(nodes[0], nodes[1]),
      makeEdge(nodes[1], nodes[2]),
      makeEdge(nodes[2], nodes[3]),
      makeEdge(nodes[3], nodes[4]),
      makeEdge(nodes[4], nodes[5]),
      makeEdge(nodes[5], nodes[6]),
      makeEdge(nodes[6], nodes[7]),
    ],
    viewport: { x: 0, y: 0, zoom: 1 },
  }
}

function makeConfigField(
  key: NodeConfigKey,
  kind: NodeConfigFieldKind,
  options: NodeConfigOption[] = []
): NodeConfigField {
  return {
    key,
    kind,
    labelKey: `canvas.nodeConfig.${key}`,
    placeholderKey: `canvas.nodeConfigPlaceholders.${key}`,
    options,
  }
}

function makeNode(type: CanvasNodeType, titleKey: string, x: number, y: number): CanvasNode {
  return {
    id: createId(type),
    type,
    title: t(titleKey),
    x,
    y,
    width: 170,
    height: 86,
    status: 'idle',
    config: {},
  }
}

function makeEdge(source: CanvasNode, target: CanvasNode) {
  return {
    id: createId('edge'),
    source_node_id: source.id,
    target_node_id: target.id,
  }
}

function createId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

function beginNewCanvas(): void {
  selectedCanvasId.value = null
  draftName.value = t('canvas.untitledCanvas')
  draftDescription.value = ''
  selectedModel.value = models.value[0]?.id ?? ''
  canvasDocument.value = createDefaultDocument()
  selectedNodeId.value = canvasDocument.value.nodes[0]?.id ?? null
  selectedEdgeId.value = null
  linkSourceNodeId.value = null
  runs.value = []
  resetCanvasTaskState()
}

async function loadCanvases(): Promise<void> {
  loadingCanvases.value = true
  canvasLoadError.value = ''
  try {
    const response = await listCanvases({ limit: 30, offset: 0 })
    canvases.value = response.items
    if (response.items.length > 0 && !selectedCanvasId.value) {
      await openCanvas(response.items[0].id)
    } else if (!selectedCanvasId.value && !draftName.value) {
      beginNewCanvas()
    }
  } catch (error: unknown) {
    canvases.value = []
    canvasLoadError.value = errorMessage(error, t('canvas.loadCanvasesFailed'))
    if (!draftName.value) {
      beginNewCanvas()
    }
  } finally {
    loadingCanvases.value = false
  }
}

async function loadModels(): Promise<void> {
  const requestId = ++modelsRequestId
  loadingModels.value = true
  try {
    const response = await listCanvasModels(selectedKeyId.value)
    if (requestId !== modelsRequestId) return
    models.value = response.items
    if (!selectedNode.value || selectedNodeApiKeyId.value === selectedKeyId.value) {
      ++selectedNodeModelsRequestId
      selectedNodeModels.value = response.items
    }
    if (!response.items.some((item) => item.id === selectedModel.value)) {
      selectedModel.value = response.items[0]?.id ?? ''
    }
  } catch {
    if (requestId === modelsRequestId) {
      models.value = []
      if (!selectedNode.value || selectedNodeApiKeyId.value === selectedKeyId.value) {
        ++selectedNodeModelsRequestId
        selectedNodeModels.value = []
      }
    }
  } finally {
    if (requestId === modelsRequestId) loadingModels.value = false
  }
}

async function refreshRunOptions(): Promise<void> {
  await Promise.all([loadApiKeys(), loadModels()])
}

async function loadApiKeys(): Promise<void> {
  loadingKeys.value = true
  try {
    const response = await keysAPI.list(1, 100, {
      status: 'active',
      sort_by: 'created_at',
      sort_order: 'desc',
    })
    apiKeys.value = response.items
    selectedKeyId.value = pickDefaultApiKey(imageApiKeys.value)?.id ?? null
  } catch {
    apiKeys.value = []
    selectedKeyId.value = null
    appStore.showError(t('canvas.loadKeysFailed'))
  } finally {
    loadingKeys.value = false
  }
}

watch(selectedKeyId, async (keyID, previousKeyID) => {
  if (keyID === previousKeyID) return
  await loadModels()
})

watch(selectedNodeId, () => {
  void loadSelectedNodeModels()
})

async function openCanvas(id: string): Promise<void> {
  loadingCanvas.value = true
  selectedCanvasId.value = id
  resetCanvasTaskState()
  try {
    const item = await getCanvas(id)
    applyCanvas(item)
    await loadRuns(id)
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.openFailed')))
  } finally {
    loadingCanvas.value = false
  }
}

async function loadRuns(canvasId: string): Promise<void> {
  try {
    const response = await listCanvasRuns({ canvas_id: canvasId, limit: 8, offset: 0 })
    runs.value = response.items
    syncCanvasImageTasksFromRuns(response.items)
  } catch {
    runs.value = []
    syncCanvasImageTasksFromRuns([])
  }
}

async function saveCanvas(): Promise<void> {
  await persistCanvas(true)
}

async function persistCanvas(notify: boolean): Promise<UserCanvas | null> {
  if (!canSave.value) return null
  saving.value = true
  try {
    const payload = {
      name: draftName.value.trim(),
      description: draftDescription.value.trim() || undefined,
      model: selectedModel.value || undefined,
      document: canvasDocument.value,
    }
    const saved = selectedCanvasId.value
      ? await updateCanvas(selectedCanvasId.value, payload)
      : await createCanvas(payload)
    applyCanvas(saved)
    upsertCanvasSummary(saved)
    if (notify) {
      appStore.showSuccess(t('canvas.saveSuccess'))
    }
    return saved
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.saveFailed')))
    return null
  } finally {
    saving.value = false
  }
}

async function queueCanvasRun(): Promise<void> {
  if (!selectedKey.value) {
    appStore.showError(t('canvas.selectApiKeyFirst'))
    return
  }
  if (!canQueueRun.value) return
  queuingRun.value = true
  try {
    const saved = await persistCanvas(false)
    if (!saved) return
    const canvasId = saved.id
    const run = await createCanvasRun({
      canvas_id: canvasId,
      api_key_id: selectedKey.value.id,
      model: selectedModel.value || undefined,
    })
    runs.value = [run, ...runs.value].slice(0, 8)
    syncCanvasImageTasksFromRuns(runs.value)
    await loadRuns(canvasId)
    appStore.showSuccess(t('canvas.runQueued'))
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.queueFailed')))
  } finally {
    queuingRun.value = false
  }
}

async function uploadSelectedNodeReferenceImage(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0] ?? null
  input.value = ''
  if (!file || !selectedNode.value || !selectedNodeApiKey.value) return
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type.toLowerCase())) {
    appStore.showError(t('canvas.invalidReferenceImage'))
    return
  }
  uploadingReferenceImage.value = true
  try {
    const image = await uploadImageCreatorReference({ apiKeyId: selectedNodeApiKey.value.id, file })
    const nextConfig = { ...(selectedNode.value.config ?? {}) }
    nextConfig.referenceImageId = String(image.id)
    nextConfig.referenceImageName = file.name
    nextConfig.referenceImageUrl = image.url
    selectedNode.value.config = nextConfig
    await resolveCanvasImagePreview(image.url)
    appStore.showSuccess(t('canvas.referenceImageUploaded'))
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.referenceImageUploadFailed')))
  } finally {
    uploadingReferenceImage.value = false
  }
}

async function cancelRun(run: CanvasRun): Promise<void> {
  if (!canCancelRun(run) || cancelingRunIds.value.has(run.id)) return
  cancelingRunIds.value = new Set(cancelingRunIds.value).add(run.id)
  try {
    const canceled = await cancelCanvasRun(run.id)
    upsertRun(canceled)
    syncCanvasImageTasksFromRuns(runs.value)
    appStore.showSuccess(t('canvas.runCanceled'))
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.cancelRunFailed')))
  } finally {
    const next = new Set(cancelingRunIds.value)
    next.delete(run.id)
    cancelingRunIds.value = next
  }
}

async function cancelActiveCanvasTasks(): Promise<void> {
  if (cancelingActiveTasks.value || !activeCanvasRun.value) return
  cancelingActiveTasks.value = true
  try {
    const canceled = await cancelCanvasRun(activeCanvasRun.value.id)
    upsertRun(canceled)
    if (selectedCanvasId.value) await loadRuns(selectedCanvasId.value)
    appStore.showSuccess(t('canvas.runCanceled'))
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.cancelRunFailed')))
  } finally {
    cancelingActiveTasks.value = false
  }
}

async function removeCanvas(item: UserCanvasSummary): Promise<void> {
  if (deletingCanvasIds.value.has(item.id)) return
  if (!window.confirm(t('canvas.deleteCanvasConfirm', { name: item.name }))) return
  deletingCanvasIds.value = new Set(deletingCanvasIds.value).add(item.id)
  try {
    await deleteCanvas(item.id)
    canvases.value = canvases.value.filter((canvas) => canvas.id !== item.id)
    if (selectedCanvasId.value === item.id) {
      if (canvases.value[0]) await openCanvas(canvases.value[0].id)
      else beginNewCanvas()
    }
    appStore.showSuccess(t('canvas.deleteCanvasSuccess'))
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.deleteCanvasFailed')))
  } finally {
    const next = new Set(deletingCanvasIds.value)
    next.delete(item.id)
    deletingCanvasIds.value = next
  }
}

function upsertRun(run: CanvasRun): void {
  const index = runs.value.findIndex((item) => item.id === run.id)
  if (index >= 0) {
    runs.value.splice(index, 1, run)
    return
  }
  runs.value = [run, ...runs.value].slice(0, 8)
}

function applyCanvas(item: UserCanvas): void {
  const previousNodeId = selectedNodeId.value
  selectedCanvasId.value = item.id
  draftName.value = item.name
  draftDescription.value = item.description || ''
  selectedModel.value = item.model || selectedModel.value
  canvasDocument.value = normalizeDocument(item.document)
  selectedNodeId.value = canvasDocument.value.nodes.some((node) => node.id === previousNodeId)
    ? previousNodeId
    : canvasDocument.value.nodes[0]?.id ?? null
  selectedEdgeId.value = null
  linkSourceNodeId.value = null
}

function normalizeDocument(document: CanvasDocument | null | undefined): CanvasDocument {
  if (!document || !Array.isArray(document.nodes) || !Array.isArray(document.edges)) {
    return createDefaultDocument()
  }
  return {
    ...document,
    nodes: document.nodes.map((node) => ({
      ...node,
      status: normalizeNodeStatus(node.status),
      config: isRecord(node.config) ? node.config : {},
    })),
    edges: document.edges,
    viewport: normalizeViewport(document.viewport),
  }
}

function upsertCanvasSummary(item: UserCanvas): void {
  const summary: UserCanvasSummary = {
    id: item.id,
    name: item.name,
    description: item.description,
    node_count: item.node_count ?? item.document.nodes.length,
    run_count: item.run_count,
    thumbnail_url: item.thumbnail_url,
    created_at: item.created_at,
    updated_at: item.updated_at,
  }
  const index = canvases.value.findIndex((canvas) => canvas.id === item.id)
  if (index >= 0) {
    canvases.value.splice(index, 1, summary)
  } else {
    canvases.value.unshift(summary)
  }
}

function addNode(type: CanvasNodeType): void {
  const index = canvasDocument.value.nodes.length
  const node = makeNode(type, `canvas.nodeTypes.${type}`, 80 + (index % 4) * 210, 90 + Math.floor(index / 4) * 140)
  canvasDocument.value.nodes.push(node)
  selectedNodeId.value = node.id
  selectedEdgeId.value = null
}

function removeSelectedNode(): void {
  const id = selectedNodeId.value
  if (!id) return
  canvasDocument.value.nodes = canvasDocument.value.nodes.filter((node) => node.id !== id)
  canvasDocument.value.edges = canvasDocument.value.edges.filter((edge) =>
    edge.source_node_id !== id && edge.target_node_id !== id
  )
  if (linkSourceNodeId.value === id) linkSourceNodeId.value = null
  selectedEdgeId.value = null
  selectedNodeId.value = canvasDocument.value.nodes[0]?.id ?? null
}

function toggleEdgeCreation(): void {
  if (!selectedNode.value) return
  linkSourceNodeId.value = linkSourceNodeId.value === selectedNode.value.id ? null : selectedNode.value.id
  selectedEdgeId.value = null
}

function selectOrConnectNode(nodeId: string): void {
  if (linkSourceNodeId.value && linkSourceNodeId.value !== nodeId) {
    createEdge(linkSourceNodeId.value, nodeId)
    selectedNodeId.value = nodeId
    linkSourceNodeId.value = null
    return
  }
  selectedNodeId.value = nodeId
  selectedEdgeId.value = null
}

function createEdge(sourceNodeId: string, targetNodeId: string): void {
  if (sourceNodeId === targetNodeId) return
  const source = canvasDocument.value.nodes.find((node) => node.id === sourceNodeId)
  const target = canvasDocument.value.nodes.find((node) => node.id === targetNodeId)
  if (!source || !target) return
  const existing = canvasDocument.value.edges.find((edge) =>
    edge.source_node_id === sourceNodeId && edge.target_node_id === targetNodeId
  )
  if (existing) {
    selectedEdgeId.value = existing.id
    return
  }
  const edge = makeEdge(source, target)
  canvasDocument.value.edges.push(edge)
  selectedEdgeId.value = edge.id
}

function selectEdge(edgeId: string): void {
  selectedEdgeId.value = edgeId
  selectedNodeId.value = null
  linkSourceNodeId.value = null
}

function removeSelectedEdge(): void {
  const id = selectedEdgeId.value
  if (!id) return
  canvasDocument.value.edges = canvasDocument.value.edges.filter((edge) => edge.id !== id)
  selectedEdgeId.value = null
}

function canCancelRun(run: CanvasRun): boolean {
  return run.status === 'pending' || run.status === 'queued' || run.status === 'running'
}

function updateSelectedNodeTitleFromEvent(event: Event): void {
  const node = selectedNode.value
  if (!node) return
  const value = inputValue(event)
  node.title = value
}

function selectedNodeConfigValue(key: NodeConfigKey): string {
  const value = selectedNode.value?.config?.[key]
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

function updateSelectedNodeConfigFromEvent(key: NodeConfigKey, event: Event): void {
  updateSelectedNodeConfig(key, inputValue(event))
}

function updateSelectedNodeConfig(key: NodeConfigKey, value: string): void {
  const node = selectedNode.value
  if (!node) return
  const nextConfig = { ...(node.config ?? {}) }
  const normalized = value.trim()
  if (normalized) {
    nextConfig[key] = normalized
  } else {
    delete nextConfig[key]
  }
  node.config = nextConfig
}

function setSelectedNodeApiKeyFromEvent(event: Event): void {
  const node = selectedNode.value
  if (!node) return
  const keyID = positiveIntegerFromUnknown(inputValue(event))
  const nextConfig = { ...(node.config ?? {}) }
  if (keyID) {
    nextConfig.apiKeyId = keyID
  } else {
    delete nextConfig.apiKeyId
  }
  // A model belongs to the previous Key's groups. Reset it before loading the
  // selected Key's Canvas catalog to prevent an invalid model/key pair.
  delete nextConfig.model
  node.config = nextConfig
  void loadSelectedNodeModels()
}

async function loadSelectedNodeModels(): Promise<void> {
  const requestId = ++selectedNodeModelsRequestId
  if (!selectedNode.value || !selectedNodeSupportsApiKey.value) {
    selectedNodeModels.value = models.value
    return
  }
  try {
    const response = await listCanvasModels(selectedNodeApiKeyId.value)
    if (requestId !== selectedNodeModelsRequestId) return
    selectedNodeModels.value = response.items
  } catch {
    if (requestId === selectedNodeModelsRequestId) selectedNodeModels.value = []
  }
}

function setSelectedNodeDimensionMode(mode: string): void {
  const node = selectedNode.value
  if (!node || !selectedNodeSupportsImageDimensions.value) return
  const nextConfig: Record<string, unknown> = { ...(node.config ?? {}), dimensionMode: mode }
  if (mode === 'auto') {
    nextConfig.size = 'auto'
  } else if (mode === 'ratio') {
    nextConfig.size = canvasSizeForResolutionAndRatio(selectedNodeResolution.value, selectedNodeAspectRatio.value)
  } else {
    const width = canvasNodePositiveIntegerConfigFrom(node, 'width', 1024)
    const height = canvasNodePositiveIntegerConfigFrom(node, 'height', 1024)
    if (!canvasDimensionValidationError(width, height)) nextConfig.size = `${width}x${height}`
  }
  node.config = nextConfig
}

function setSelectedNodeResolution(resolution: string): void {
  updateSelectedNodeDimensionRatioConfig({ resolution })
}

function setSelectedNodeAspectRatio(aspectRatio: string): void {
  updateSelectedNodeDimensionRatioConfig({ aspectRatio })
}

function updateSelectedNodeDimensionRatioConfig(values: Record<string, string>): void {
  const node = selectedNode.value
  if (!node || !selectedNodeSupportsImageDimensions.value) return
  const nextConfig: Record<string, unknown> = { ...(node.config ?? {}), dimensionMode: 'ratio', ...values }
  const configuredResolution = nextConfig.resolution
  const configuredAspectRatio = nextConfig.aspectRatio
  const resolution = values.resolution ?? (configuredResolution === '2K' || configuredResolution === '4K' ? configuredResolution : '1K')
  const aspectRatio = values.aspectRatio ?? (typeof configuredAspectRatio === 'string' ? configuredAspectRatio : '1:1')
  nextConfig.size = canvasSizeForResolutionAndRatio(resolution, aspectRatio)
  node.config = nextConfig
}

function setSelectedNodeCustomDimension(dimension: 'width' | 'height', event: Event): void {
  const node = selectedNode.value
  if (!node || !selectedNodeSupportsImageDimensions.value) return
  const nextConfig: Record<string, unknown> = { ...(node.config ?? {}), dimensionMode: 'custom' }
  const value = Number(inputValue(event))
  nextConfig[dimension] = Number.isFinite(value) ? Math.trunc(value) : 0
  const width = canvasNodePositiveIntegerConfigFrom({ ...node, config: nextConfig }, 'width', 0)
  const height = canvasNodePositiveIntegerConfigFrom({ ...node, config: nextConfig }, 'height', 0)
  if (!canvasDimensionValidationError(width, height)) nextConfig.size = `${width}x${height}`
  node.config = nextConfig
}

function canvasNodePositiveIntegerConfig(key: string, fallback: number): number {
  return selectedNode.value ? canvasNodePositiveIntegerConfigFrom(selectedNode.value, key, fallback) : fallback
}

function canvasNodePositiveIntegerConfigFrom(node: CanvasNode, key: string, fallback: number): number {
  const value = Number(node.config?.[key])
  return Number.isInteger(value) && value > 0 ? value : fallback
}

function canvasDimensionValidationError(width: number, height: number): string {
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 16 || height < 16) {
    return t('canvas.nodeConfig.invalidDimensions')
  }
  if (width > 3840 || height > 3840 || width % 16 !== 0 || height % 16 !== 0) {
    return t('canvas.nodeConfig.invalidDimensions')
  }
  if (Math.max(width, height) / Math.min(width, height) > 3 || width * height > 8_294_400) {
    return t('canvas.nodeConfig.invalidDimensions')
  }
  return ''
}

function canvasNodeHasInvalidCustomDimensions(node: CanvasNode): boolean {
  const mode = node.config?.dimensionMode
  if (mode !== 'custom') return false
  return Boolean(canvasDimensionValidationError(
    canvasNodePositiveIntegerConfigFrom(node, 'width', 0),
    canvasNodePositiveIntegerConfigFrom(node, 'height', 0),
  ))
}

function canvasSizeForResolutionAndRatio(resolution: string, aspectRatio: string): string {
  const [ratioWidth, ratioHeight] = aspectRatio.split(':').map(Number)
  const safeRatioWidth = ratioWidth > 0 ? ratioWidth : 1
  const safeRatioHeight = ratioHeight > 0 ? ratioHeight : 1
  const isSquare = safeRatioWidth === safeRatioHeight
  // Image tiers use model presets: 1K landscape/portrait outputs use a
  // 1536px long edge (for example, 1536x1024 at 3:2), while square 1K is
  // 1024x1024.  4K is capped by the gateway's 8,294,400-pixel limit.
  const base = resolution === '4K' ? 3840 : resolution === '2K' ? 2048 : isSquare ? 1024 : 1536
  // Keep the requested tier as the long-edge target, while respecting the
  // gateway's 8,294,400-pixel limit (4K square would otherwise be invalid).
  const maxPixels = 8_294_400
  const scale = Math.min(
    base / Math.max(safeRatioWidth, safeRatioHeight),
    Math.sqrt(maxPixels / (safeRatioWidth * safeRatioHeight)),
  )
  let width = Math.max(16, Math.round((safeRatioWidth * scale) / 16) * 16)
  let height = Math.max(16, Math.round((safeRatioHeight * scale) / 16) * 16)
  while (width * height > maxPixels) {
    if (width >= height) width -= 16
    else height -= 16
  }
  return `${width}x${height}`
}

function canvasAspectRatioShapeStyle(aspectRatio: string): Record<string, string> {
  const [width, height] = aspectRatio.split(':').map(Number)
  const scale = 24 / Math.max(width, height)
  return { width: `${Math.max(7, Math.round(width * scale))}px`, height: `${Math.max(7, Math.round(height * scale))}px` }
}

function nodeTypeLabel(type: CanvasNodeType): string {
  return t(`canvas.nodeTypes.${type}`)
}

function nodeKindClass(type: CanvasNodeType): string {
  return `canvas-kind-${type.replace(/_/g, '-')}`
}

function nodeStyle(node: CanvasNode): Record<string, string> {
  return {
    left: `${node.x}px`,
    top: `${node.y}px`,
    width: `${Math.max(node.width || 190, 190)}px`,
    minHeight: `${node.height || 112}px`,
  }
}

function startNodeDrag(node: CanvasNode, event: MouseEvent): void {
  if (event.button !== 0) return
  selectedNodeId.value = node.id
  selectedEdgeId.value = null
  canvasDragState.value = {
    nodeId: node.id,
    startClientX: event.clientX,
    startClientY: event.clientY,
    startNodeX: node.x,
    startNodeY: node.y,
  }
  addCanvasPointerListeners()
}

function startCanvasPan(event: MouseEvent): void {
  if (event.button !== 0) return
  const viewport = currentViewport()
  selectedEdgeId.value = null
  canvasPanState.value = {
    startClientX: event.clientX,
    startClientY: event.clientY,
    startViewportX: viewport.x,
    startViewportY: viewport.y,
  }
  addCanvasPointerListeners()
}

function handleCanvasPointerMove(event: MouseEvent): void {
  const dragState = canvasDragState.value
  if (dragState) {
    const node = canvasDocument.value.nodes.find((item) => item.id === dragState.nodeId)
    if (!node) return
    const zoom = currentViewport().zoom
    node.x = clampNumber(Math.round(dragState.startNodeX + ((event.clientX - dragState.startClientX) / zoom)), 0, canvasWorldSize.width - (node.width || 190))
    node.y = clampNumber(Math.round(dragState.startNodeY + ((event.clientY - dragState.startClientY) / zoom)), 0, canvasWorldSize.height - (node.height || 112))
    return
  }
  const panState = canvasPanState.value
  if (panState) {
    const viewport = currentViewport()
    viewport.x = Math.round(panState.startViewportX + event.clientX - panState.startClientX)
    viewport.y = Math.round(panState.startViewportY + event.clientY - panState.startClientY)
  }
}

function handleCanvasPointerUp(): void {
  canvasDragState.value = null
  canvasPanState.value = null
  removeCanvasPointerListeners()
}

function addCanvasPointerListeners(): void {
  if (canvasPointerListenersActive) return
  canvasPointerListenersActive = true
  window.addEventListener('mousemove', handleCanvasPointerMove)
  window.addEventListener('mouseup', handleCanvasPointerUp)
}

function removeCanvasPointerListeners(): void {
  if (!canvasPointerListenersActive) return
  canvasPointerListenersActive = false
  window.removeEventListener('mousemove', handleCanvasPointerMove)
  window.removeEventListener('mouseup', handleCanvasPointerUp)
}

function handleCanvasWheel(event: WheelEvent): void {
  const nextZoom = currentViewport().zoom * (event.deltaY > 0 ? 0.9 : 1.1)
  setCanvasZoom(nextZoom)
}

function zoomCanvasBy(multiplier: number): void {
  setCanvasZoom(currentViewport().zoom * multiplier)
}

function setCanvasZoom(zoom: number): void {
  currentViewport().zoom = clampNumber(Number(zoom.toFixed(2)), 0.35, 2)
}

function fitCanvasView(): void {
  const stage = stageRef.value
  const viewport = currentViewport()
  const bounds = canvasNodeBounds()
  if (!stage || !bounds) {
    viewport.x = 0
    viewport.y = 0
    viewport.zoom = 1
    return
  }
  const padding = 48
  const width = Math.max(bounds.maxX - bounds.minX, 1)
  const height = Math.max(bounds.maxY - bounds.minY, 1)
  const zoom = clampNumber(Math.min(
    (stage.clientWidth - padding * 2) / width,
    (stage.clientHeight - padding * 2) / height,
    1
  ), 0.35, 1.4)
  viewport.zoom = Number(zoom.toFixed(2))
  viewport.x = Math.round((stage.clientWidth - width * viewport.zoom) / 2 - bounds.minX * viewport.zoom)
  viewport.y = Math.round((stage.clientHeight - height * viewport.zoom) / 2 - bounds.minY * viewport.zoom)
}

function canvasNodeBounds(): { minX: number, minY: number, maxX: number, maxY: number } | null {
  if (canvasDocument.value.nodes.length === 0) return null
  return canvasDocument.value.nodes.reduce((bounds, node) => ({
    minX: Math.min(bounds.minX, node.x),
    minY: Math.min(bounds.minY, node.y),
    maxX: Math.max(bounds.maxX, node.x + (node.width || 190)),
    maxY: Math.max(bounds.maxY, node.y + (node.height || 112)),
  }), {
    minX: Number.POSITIVE_INFINITY,
    minY: Number.POSITIVE_INFINITY,
    maxX: Number.NEGATIVE_INFINITY,
    maxY: Number.NEGATIVE_INFINITY,
  })
}

function currentViewport(): CanvasViewport {
  if (!canvasDocument.value.viewport) {
    canvasDocument.value.viewport = { ...canvasViewportDefaults }
  }
  return canvasDocument.value.viewport
}

function normalizeViewport(viewport: CanvasDocument['viewport']): CanvasViewport {
  if (!viewport) return { ...canvasViewportDefaults }
  return {
    x: finiteNumberOrDefault(viewport.x, canvasViewportDefaults.x),
    y: finiteNumberOrDefault(viewport.y, canvasViewportDefaults.y),
    zoom: clampNumber(finiteNumberOrDefault(viewport.zoom, canvasViewportDefaults.zoom), 0.35, 2),
  }
}

function finiteNumberOrDefault(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function clampNumber(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max)
}

function nodeDisplayStatus(node: CanvasNode): CanvasNodeStatus {
  if (node.type === 'result') {
    const upstreamTask = resultNodeUpstreamImageTask(node)
    if (upstreamTask) return canvasNodeStatusFromTaskStatus(upstreamTask.status)
  }
  const taskLink = imageTaskLinkForNode(node.id)
  if (taskLink) {
    return canvasNodeStatusFromTaskStatus(imageTaskStatusForNode(node.id) ?? 'pending')
  }
  if (node.status && node.status !== 'idle') return normalizeNodeStatus(node.status)
  if (nodeErrorSummary(node)) return 'failed'
  if (node.result !== undefined || outputForNode(node) !== undefined) return 'done'
  const config = node.config ?? {}
  if ((node.type === 'prompt' && stringFromUnknown(config.prompt)) ||
    (node.type === 'text' && stringFromUnknown(config.text)) ||
    (node.type === 'image' && positiveIntegerFromUnknown(config.referenceImageId))) {
    return 'done'
  }
  return 'idle'
}

function nodeResultImageUrl(node: CanvasNode): string {
  if (node.type === 'result') {
    const task = resultNodeUpstreamImageTask(node)
    return task ? firstImageUrl(imageTaskToNodeOutput(task)) : ''
  }
  const taskOutput = canvasTaskOutputForNode(node)
  if (taskOutput !== undefined) return firstImageUrl(taskOutput)
  return firstImageUrl(node.result) || firstImageUrl(outputForNode(node))
}

function nodeResultPreviewUrl(node: CanvasNode): string {
  const imageUrl = nodeResultImageUrl(node)
  return imageUrl ? imagePreviewUrls.value[imageUrl] || '' : ''
}

async function openImagePreview(node: CanvasNode): Promise<void> {
  const sourceUrl = nodeResultImageUrl(node)
  if (!sourceUrl) return
  const imageUrl = nodeResultPreviewUrl(node) || await resolveCanvasImagePreview(sourceUrl)
  if (!imageUrl) return
  previewImageSourceUrl.value = sourceUrl
  previewImageUrl.value = imageUrl
  previewImageName.value = node.title || t('canvas.imagePreview')
}

function closeImagePreview(): void {
  previewImageUrl.value = ''
  previewImageName.value = ''
  previewImageSourceUrl.value = ''
}

async function downloadNodeImage(node: CanvasNode): Promise<void> {
  const imageUrl = nodeResultImageUrl(node)
  if (!imageUrl || downloadingImage.value) return
  downloadingImage.value = true
  try {
    const blob = await downloadImageFile(imageUrl)
    const extension = imageFileExtension(blob.type, imageUrl)
    const objectUrl = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = objectUrl
    anchor.download = `${node.title || 'canvas-image'}.${extension}`
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(objectUrl)
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('canvas.downloadImageFailed')))
  } finally {
    downloadingImage.value = false
  }
}

function downloadPreviewImage(): Promise<void> {
  const node = canvasDocument.value.nodes.find((candidate) => nodeResultImageUrl(candidate) === previewImageSourceUrl.value)
  return node ? downloadNodeImage(node) : Promise.resolve()
}

async function preloadCanvasImagePreviews(tasks: Array<ImageCreatorTask | null>): Promise<void> {
  const urls = tasks.flatMap((task) => task?.images?.map((image) => image.url) ?? []).filter(Boolean)
  await Promise.all(urls.map((url) => resolveCanvasImagePreview(url)))
}

async function resolveCanvasImagePreview(imageUrl: string): Promise<string> {
  if (!imageUrl) return ''
  const cached = imagePreviewUrls.value[imageUrl]
  if (cached) return cached
  const inFlight = imagePreviewLoads.get(imageUrl)
  if (inFlight) return inFlight
  const load = downloadImageFile(imageUrl)
    .then((blob) => {
      const objectUrl = URL.createObjectURL(blob)
      imagePreviewUrls.value = { ...imagePreviewUrls.value, [imageUrl]: objectUrl }
      return objectUrl
    })
    .catch(() => '')
    .finally(() => imagePreviewLoads.delete(imageUrl))
  imagePreviewLoads.set(imageUrl, load)
  return load
}

function clearCanvasImagePreviews(): void {
  for (const objectUrl of Object.values(imagePreviewUrls.value)) URL.revokeObjectURL(objectUrl)
  imagePreviewUrls.value = {}
  imagePreviewLoads.clear()
}

function imageFileExtension(mimeType: string, imageUrl: string): string {
  if (mimeType === 'image/png') return 'png'
  if (mimeType === 'image/jpeg') return 'jpeg'
  if (mimeType === 'image/webp') return 'webp'
  const match = imageUrl.match(/\.(png|jpe?g|webp)(?:\?|$)/i)
  return match?.[1]?.toLowerCase() === 'jpg' ? 'jpeg' : match?.[1]?.toLowerCase() || 'png'
}

function nodeResultSummary(node: CanvasNode): string {
  if (node.type === 'result') {
    const task = resultNodeUpstreamImageTask(node)
    return task ? summarizeUnknown(imageTaskToNodeOutput(task)) : ''
  }
  const taskOutput = canvasTaskOutputForNode(node)
  if (taskOutput !== undefined) return summarizeUnknown(taskOutput)
  const result = node.result ?? outputForNode(node)
  return summarizeUnknown(result)
}

function nodeErrorSummary(node: CanvasNode): string {
  if (node.type === 'result') {
    const task = resultNodeUpstreamImageTask(node)
    return task?.error_message || ''
  }
  const task = imageTaskForNode(node.id)
  if (task?.error_message) return task.error_message
  const output = outputForNode(node)
  return summarizeUnknown(node.error) || summarizeUnknown(node.config?.error) ||
    (isRecord(output) ? summarizeUnknown(output.error) : '')
}

function outputForNode(node: CanvasNode): unknown {
  if (node.type === 'result') {
    const task = resultNodeUpstreamImageTask(node)
    return task ? imageTaskToNodeOutput(task) : undefined
  }
  const taskOutput = canvasTaskOutputForNode(node)
  if (taskOutput !== undefined) return taskOutput
  const outputs = latestRun.value ? runOutputs(latestRun.value) : {}
  if (!outputs) return undefined
  return outputs[node.id]
}

function runOutputSummary(run: CanvasRun): string {
  if (run.error_message) return ''
  const imageTaskLinks = canvasImageTaskLinksFromRun(run)
  if (imageTaskLinks.length > 0) {
    return t('canvas.imageTaskSummary', { count: imageTaskLinks.length })
  }
  const outputs = runOutputs(run)
  const resultNodeId = run.result_node_ids?.[0]
  if (resultNodeId && outputs[resultNodeId] !== undefined) {
    return summarizeUnknown(outputs[resultNodeId])
  }
  return summarizeUnknown(outputs)
}

function runOutputs(run: CanvasRun): Record<string, unknown> {
  if (run.outputs && Object.keys(run.outputs).length > 0) return run.outputs
  return isRecord(run.output) ? run.output : {}
}

function firstImageUrl(value: unknown): string {
  if (typeof value === 'string') {
    return isImageLikeUrl(value) ? value : ''
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = firstImageUrl(item)
      if (found) return found
    }
    return ''
  }
  if (!isRecord(value)) return ''
  for (const key of ['thumbnail_url', 'thumbnailUrl', 'image_url', 'imageUrl', 'url', 'src']) {
    const raw = value[key]
    if (typeof raw === 'string' && isImageLikeUrl(raw)) {
      return raw
    }
  }
  for (const key of ['images', 'items', 'output', 'result']) {
    const found = firstImageUrl(value[key])
    if (found) return found
  }
  return ''
}

function isImageLikeUrl(value: string): boolean {
  return /^(https?:|data:image\/|blob:)/i.test(value) ||
    value.startsWith('/api/v1/user/image-creator/images/') ||
    /\.(png|jpe?g|webp|gif)(\?.*)?$/i.test(value)
}

function summarizeUnknown(value: unknown): string {
  if (value === undefined || value === null || value === '') return ''
  if (typeof value === 'string') return truncateText(value)
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value)) {
    const text = value.map((item) => summarizeUnknown(item)).filter(Boolean).join(' · ')
    return truncateText(text)
  }
  if (!isRecord(value)) return ''
  for (const key of ['summary', 'message', 'error', 'text', 'prompt', 'title', 'id']) {
    const raw = value[key]
    if (typeof raw === 'string' && raw.trim()) {
      return truncateText(raw)
    }
  }
  const imageUrl = firstImageUrl(value)
  if (imageUrl) return t('canvas.imageResult')
  const keys = Object.keys(value)
  return keys.length > 0 ? truncateText(keys.slice(0, 3).join(', ')) : ''
}

function truncateText(value: string): string {
  const normalized = value.replace(/\s+/g, ' ').trim()
  return normalized.length > 72 ? `${normalized.slice(0, 69)}...` : normalized
}

function normalizeNodeStatus(status: CanvasNode['status']): CanvasNodeStatus {
  return status === 'queued' || status === 'running' || status === 'done' || status === 'failed' ? status : 'idle'
}

function resetCanvasTaskState(): void {
  canvasTaskSyncVersion += 1
  canvasTaskLinks.value = []
  canvasTasksById.value = {}
  clearCanvasImagePreviews()
  stopCanvasTaskPolling()
}

function syncCanvasImageTasksFromRuns(sourceRuns: CanvasRun[]): void {
  canvasTaskSyncVersion += 1
  const nextLinks = canvasImageTaskLinksFromRuns(sourceRuns)
  canvasTaskLinks.value = nextLinks
  const taskIds = new Set(nextLinks.map((link) => String(link.taskId)))
  const retainedTasks: Record<string, ImageCreatorTask> = {}
  for (const [taskId, task] of Object.entries(canvasTasksById.value)) {
    if (taskIds.has(taskId)) retainedTasks[taskId] = task
  }
  canvasTasksById.value = retainedTasks

  const idsToFetch = Array.from(taskIds).map((taskId) => Number(taskId))
  if (idsToFetch.length > 0) {
    void pollCanvasImageTasks(idsToFetch)
  } else {
    stopCanvasTaskPolling()
  }
  refreshCanvasTaskPolling()
}

function canvasImageTaskLinksFromRuns(sourceRuns: CanvasRun[]): CanvasRunImageTaskLink[] {
  const links: CanvasRunImageTaskLink[] = []
  const seenNodeIds = new Set<string>()
  for (const run of sourceRuns) {
    for (const link of canvasImageTaskLinksFromRun(run)) {
      if (seenNodeIds.has(link.nodeId)) continue
      seenNodeIds.add(link.nodeId)
      links.push(link)
    }
  }
  return links
}

function canvasImageTaskLinksFromRun(run: CanvasRun): CanvasRunImageTaskLink[] {
  const links: CanvasRunImageTaskLink[] = []
  for (const candidate of [run.output, run.outputs]) {
    if (!isRecord(candidate)) continue
    const items = Array.isArray(candidate.image_tasks)
      ? candidate.image_tasks
      : Array.isArray(candidate.imageTasks)
        ? candidate.imageTasks
        : []
    for (const item of items) {
      const link = canvasImageTaskLinkFromUnknown(item)
      if (link) links.push(link)
    }
  }
  return links
}

function canvasImageTaskLinkFromUnknown(value: unknown): CanvasRunImageTaskLink | null {
  if (!isRecord(value)) return null
  const nodeId = stringFromUnknown(value.node_id) || stringFromUnknown(value.nodeId)
  const taskId = positiveIntegerFromUnknown(value.task_id ?? value.taskId)
  if (!nodeId || taskId === null) return null
  return {
    nodeId,
    taskId,
    taskStatus: normalizeImageCreatorTaskStatus(value.task_status ?? value.taskStatus ?? value.status),
  }
}

async function pollCanvasImageTasks(taskIds = activeCanvasTaskIds()): Promise<void> {
  const ids = Array.from(new Set(taskIds.filter((taskId) => Number.isFinite(taskId) && taskId > 0)))
  if (ids.length === 0 || pollingCanvasTasks) {
    refreshCanvasTaskPolling()
    return
  }
  const syncVersion = canvasTaskSyncVersion
  pollingCanvasTasks = true
  try {
    const tasks = await Promise.all(ids.map(async (taskId) => {
      try {
        return await getImageTask(taskId)
      } catch {
        return null
      }
    }))
    if (syncVersion !== canvasTaskSyncVersion) return
    const nextTasks = { ...canvasTasksById.value }
    for (const task of tasks) {
      if (!task) continue
      nextTasks[String(task.id)] = task
    }
    canvasTasksById.value = nextTasks
    void preloadCanvasImagePreviews(tasks)
  } finally {
    pollingCanvasTasks = false
    refreshCanvasTaskPolling()
  }
}

function refreshCanvasTaskPolling(): void {
  if (activeCanvasTaskIds().length > 0) {
    startCanvasTaskPolling()
  } else {
    stopCanvasTaskPolling()
  }
}

function startCanvasTaskPolling(): void {
  if (canvasTaskPollTimerId !== null) return
  canvasTaskPollTimerId = setInterval(() => {
    void pollCanvasImageTasks()
  }, canvasTaskPollIntervalMs)
}

function stopCanvasTaskPolling(): void {
  if (canvasTaskPollTimerId === null) return
  clearInterval(canvasTaskPollTimerId)
  canvasTaskPollTimerId = null
}

function activeCanvasTaskIds(): number[] {
  const taskIds = new Set<number>()
  for (const link of canvasTaskLinks.value) {
    const task = canvasTasksById.value[String(link.taskId)]
    const status = task?.status ?? link.taskStatus
    if (!status || taskIsActiveStatus(status)) {
      taskIds.add(link.taskId)
    }
  }
  return Array.from(taskIds)
}

function imageTaskLinkForNode(nodeId: string): CanvasRunImageTaskLink | null {
  return canvasTaskLinks.value.find((link) => link.nodeId === nodeId) ?? null
}

function imageTaskForNode(nodeId: string): ImageCreatorTask | null {
  const link = imageTaskLinkForNode(nodeId)
  return link ? canvasTasksById.value[String(link.taskId)] ?? null : null
}

function resultNodeUpstreamImageTask(resultNode: CanvasNode): ImageCreatorTask | null {
  const nodesByID = new Map(canvasDocument.value.nodes.map((node) => [node.id, node]))
  const queuedNodeIDs = [resultNode.id]
  const visitedNodeIDs = new Set<string>()
  while (queuedNodeIDs.length > 0) {
    const targetID = queuedNodeIDs.shift()
    if (!targetID || visitedNodeIDs.has(targetID)) continue
    visitedNodeIDs.add(targetID)
    for (const edge of canvasDocument.value.edges) {
      if (edge.target_node_id !== targetID) continue
      const source = nodesByID.get(edge.source_node_id)
      if (!source) continue
      if (source.type === 'text_to_image' || source.type === 'image_to_image') {
        const task = imageTaskForNode(source.id)
        if (task) return task
      }
      queuedNodeIDs.push(source.id)
    }
  }
  return null
}

function imageTaskStatusForNode(nodeId: string): ImageCreatorTaskStatus | undefined {
  const link = imageTaskLinkForNode(nodeId)
  if (!link) return undefined
  return canvasTasksById.value[String(link.taskId)]?.status ?? link.taskStatus
}

function canvasTaskOutputForNode(node: CanvasNode): unknown {
  const task = imageTaskForNode(node.id)
  if (task) return imageTaskToNodeOutput(task)
  const link = imageTaskLinkForNode(node.id)
  if (!link) return undefined
  return {
    task_id: link.taskId,
    status: link.taskStatus,
    message: t('canvas.imageTaskStatusSummary', {
      status: link.taskStatus ? t(`canvas.imageTaskStatus.${link.taskStatus}`) : t('canvas.nodeStatus.queued'),
    }),
  }
}

function imageTaskToNodeOutput(task: ImageCreatorTask): Record<string, unknown> {
  const images = task.images ?? []
  if (task.status === 'failed' || task.status === 'canceled') {
    return {
      task_id: task.id,
      status: task.status,
      error: task.error_message || t('canvas.imageTaskStatusSummary', { status: t('canvas.nodeStatus.failed') }),
    }
  }
  return {
    task_id: task.id,
    status: task.status,
    images,
    summary: images.length > 0
      ? t('canvas.imageTaskDone', { count: images.length })
      : t('canvas.imageTaskStatusSummary', { status: t(`canvas.imageTaskStatus.${task.status}`) }),
    prompt: task.prompt,
    model: task.model,
  }
}

function canvasNodeStatusFromTaskStatus(status: ImageCreatorTaskStatus): CanvasNodeStatus {
  if (status === 'succeeded') return 'done'
  if (status === 'failed' || status === 'canceled') return 'failed'
  if (status === 'running') return 'running'
  return 'queued'
}

function taskIsActiveStatus(status: ImageCreatorTaskStatus | undefined): boolean {
  return status === 'pending' || status === 'running'
}

function normalizeImageCreatorTaskStatus(status: unknown): ImageCreatorTaskStatus | undefined {
  return status === 'pending' || status === 'running' || status === 'succeeded' || status === 'failed' || status === 'canceled'
    ? status
    : undefined
}

function positiveIntegerFromUnknown(value: unknown): number | null {
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) return value
  if (typeof value === 'string' && /^\d+$/.test(value.trim())) return Number(value.trim())
  return null
}

function stringFromUnknown(value: unknown): string {
  return typeof value === 'string' && value.trim() ? value.trim() : ''
}

function inputValue(event: Event): string {
  return event.target instanceof HTMLInputElement ||
    event.target instanceof HTMLTextAreaElement ||
    event.target instanceof HTMLSelectElement
    ? event.target.value
    : ''
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function canvasMeta(item: UserCanvasSummary): string {
  const parts = [
    t('canvas.nodeCount', { count: item.node_count ?? 0 }),
    formatDate(item.updated_at),
  ].filter(Boolean)
  return parts.join(' · ')
}

function modelLabel(modelItem: CanvasModel): string {
  return [displayModelLabel(modelItem.id, modelItem.name || modelItem.id), modelItem.provider].filter(Boolean).join(' · ')
}

function apiKeyLabel(key: ApiKey): string {
  return [key.name, primaryAPIKeyImageGroupName(key), 'OpenAI'].filter(Boolean).join(' · ')
}

function pickDefaultApiKey(keys: ApiKey[]): ApiKey | null {
  const current = keys.find((key) => key.id === selectedKeyId.value)
  return current ?? keys[0] ?? null
}

function runStatusLabel(status: CanvasRunStatus): string {
  return t(`canvas.runStatus.${status}`)
}

function formatDate(value?: string): string {
  if (!value) return t('common.notAvailable')
  const time = Date.parse(value)
  if (!Number.isFinite(time)) return value
  return new Intl.DateTimeFormat(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(time))
}

function errorMessage(error: unknown, fallback: string): string {
  if (typeof error === 'object' && error !== null && 'message' in error) {
    const value = (error as { message?: unknown }).message
    if (typeof value === 'string' && value.trim()) {
      return value
    }
  }
  return fallback
}
</script>

<style scoped>
.canvas-studio {
  display: grid;
  min-height: calc(100vh - 7rem);
  min-height: calc(100dvh - 7rem);
  grid-template-columns: 300px minmax(0, 1fr) 330px;
  gap: 1rem;
}

.canvas-panel,
.canvas-workspace {
  min-height: 0;
  overflow: hidden;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.5rem;
  background: rgb(255 255 255);
}

.dark .canvas-panel,
.dark .canvas-workspace {
  border-color: rgb(55 65 81);
  background: rgb(31 41 55 / 0.78);
}

.canvas-panel {
  display: flex;
  flex-direction: column;
}

.canvas-panel-header,
.canvas-toolbar,
.canvas-stage-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  border-bottom: 1px solid rgb(243 244 246);
  padding: 1rem;
}

.dark .canvas-panel-header,
.dark .canvas-toolbar,
.dark .canvas-stage-header {
  border-color: rgb(55 65 81);
}

.canvas-section {
  border-bottom: 1px solid rgb(243 244 246);
  padding: 1rem;
}

.dark .canvas-section {
  border-color: rgb(55 65 81);
}

.canvas-section:last-child {
  border-bottom: 0;
}

.canvas-section-title {
  margin-bottom: 0.75rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  font-size: 0.75rem;
  font-weight: 700;
  text-transform: uppercase;
  color: rgb(100 116 139);
}

.dark .canvas-section-title {
  color: rgb(148 163 184);
}

.canvas-section-actions,
.canvas-stage-title,
.canvas-stage-tools {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}

.canvas-stage-title {
  min-width: 0;
  flex-wrap: wrap;
}

.canvas-stage-tools {
  flex-shrink: 0;
}

.canvas-icon-button {
  display: inline-flex;
  height: 1.875rem;
  width: 1.875rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.375rem;
  color: rgb(100 116 139);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.canvas-icon-button:hover:not(:disabled) {
  background: rgb(241 245 249);
  color: rgb(15 23 42);
}

.canvas-icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.canvas-icon-button-active {
  background: rgb(204 251 241);
  color: rgb(15 118 110);
}

.dark .canvas-icon-button {
  color: rgb(148 163 184);
}

.dark .canvas-icon-button:hover:not(:disabled) {
  background: rgb(55 65 81 / 0.72);
  color: rgb(243 244 246);
}

.dark .canvas-icon-button-active {
  background: rgb(20 184 166 / 0.2);
  color: rgb(94 234 212);
}

.canvas-zoom-value {
  min-width: 3rem;
  text-align: center;
  font-size: 0.75rem;
  font-weight: 800;
  color: rgb(71 85 105);
}

.dark .canvas-zoom-value {
  color: rgb(203 213 225);
}

.canvas-list,
.canvas-node-list,
.canvas-run-list {
  max-height: 16rem;
  overflow-y: auto;
}

.canvas-list-item,
.canvas-node-list-item,
.canvas-run-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.75rem;
  border-radius: 0.5rem;
  padding: 0.625rem;
  text-align: left;
  color: rgb(51 65 85);
}

.canvas-list-item:hover,
.canvas-list-item-active,
.canvas-node-list-item:hover,
.canvas-node-list-item-active {
  background: rgb(236 253 245);
  color: rgb(15 118 110);
}

.dark .canvas-list-item,
.dark .canvas-node-list-item,
.dark .canvas-run-item {
  color: rgb(209 213 219);
}

.dark .canvas-list-item:hover,
.dark .canvas-list-item-active,
.dark .canvas-node-list-item:hover,
.dark .canvas-node-list-item-active {
  background: rgb(20 83 45 / 0.28);
  color: rgb(167 243 208);
}

.canvas-alert {
  margin-bottom: 0.75rem;
  display: flex;
  gap: 0.5rem;
  border-radius: 0.5rem;
  border: 1px solid rgb(254 215 170);
  background: rgb(255 247 237);
  padding: 0.625rem;
  font-size: 0.75rem;
  line-height: 1.25rem;
  color: rgb(154 52 18);
}

.dark .canvas-alert {
  border-color: rgb(154 52 18 / 0.5);
  background: rgb(124 45 18 / 0.22);
  color: rgb(253 186 116);
}

.canvas-empty-list,
.canvas-placeholder,
.canvas-stage-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  border-radius: 0.5rem;
  color: rgb(100 116 139);
  text-align: center;
}

.canvas-empty-list,
.canvas-placeholder {
  min-height: 7rem;
  border: 1px dashed rgb(203 213 225);
  padding: 1rem;
  font-size: 0.8125rem;
}

.canvas-compact-placeholder {
  min-height: 4.5rem;
}

.dark .canvas-empty-list,
.dark .canvas-placeholder {
  border-color: rgb(75 85 99);
  color: rgb(156 163 175);
}

.canvas-workspace {
  display: flex;
  flex-direction: column;
}

.canvas-title-input {
  width: 100%;
  border: 0;
  background: transparent;
  font-size: 1.125rem;
  font-weight: 700;
  color: rgb(17 24 39);
  outline: none;
}

.canvas-description-input {
  margin-top: 0.25rem;
  width: 100%;
  border: 0;
  background: transparent;
  font-size: 0.875rem;
  color: rgb(100 116 139);
  outline: none;
}

.dark .canvas-title-input {
  color: rgb(255 255 255);
}

.dark .canvas-description-input {
  color: rgb(156 163 175);
}

.canvas-toolbar-actions {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.5rem;
}

.canvas-latest-run {
  display: flex;
  min-height: 2.5rem;
  align-items: center;
  gap: 0.5rem;
  border-bottom: 1px solid rgb(243 244 246);
  padding: 0.625rem 1rem;
  font-size: 0.75rem;
  color: rgb(71 85 105);
}

.dark .canvas-latest-run {
  border-color: rgb(55 65 81);
  color: rgb(203 213 225);
}

.canvas-stage-shell {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
}

.canvas-stage-header {
  padding-top: 0.75rem;
  padding-bottom: 0.75rem;
  font-size: 0.75rem;
  font-weight: 700;
  color: rgb(100 116 139);
}

.dark .canvas-stage-header {
  color: rgb(148 163 184);
}

.canvas-stage {
  position: relative;
  min-height: 620px;
  flex: 1;
  overflow: auto;
  overscroll-behavior: contain;
  background-color: rgb(248 250 252);
  background-image:
    linear-gradient(rgb(226 232 240 / 0.72) 1px, transparent 1px),
    linear-gradient(90deg, rgb(226 232 240 / 0.72) 1px, transparent 1px);
  background-size: 28px 28px;
  cursor: grab;
  user-select: none;
}

.canvas-stage-panning {
  cursor: grabbing;
}

.dark .canvas-stage {
  background-color: rgb(15 23 42);
  background-image:
    linear-gradient(rgb(51 65 85 / 0.58) 1px, transparent 1px),
    linear-gradient(90deg, rgb(51 65 85 / 0.58) 1px, transparent 1px);
}

.canvas-edges {
  position: absolute;
  left: 0;
  top: 0;
  height: 100%;
  width: 100%;
  overflow: visible;
}

.canvas-stage-content {
  position: absolute;
  left: 0;
  top: 0;
  transform-origin: 0 0;
}

.canvas-edge {
  fill: none;
  stroke: rgb(20 184 166);
  stroke-linecap: round;
  stroke-width: 2;
  pointer-events: stroke;
  cursor: pointer;
}

.canvas-edge:hover,
.canvas-edge-selected {
  stroke: rgb(236 72 153);
  stroke-width: 3;
}

.canvas-node {
  position: absolute;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.35rem;
  border: 1px solid currentColor;
  border-radius: 0.5rem;
  background: rgb(255 255 255);
  padding: 0.75rem;
  text-align: left;
  box-shadow: 0 12px 28px rgb(15 23 42 / 0.1);
  cursor: move;
  transition: box-shadow 0.15s ease, transform 0.15s ease;
}

.canvas-node:hover,
.canvas-node-selected,
.canvas-node-link-source {
  box-shadow: 0 18px 38px rgb(15 23 42 / 0.16);
  transform: translateY(-1px);
}

.canvas-node-link-source {
  outline: 2px solid rgb(20 184 166);
  outline-offset: 3px;
}

.dark .canvas-node {
  background: rgb(17 24 39);
  box-shadow: 0 16px 32px rgb(0 0 0 / 0.24);
}

.canvas-node-kind {
  font-size: 0.6875rem;
  font-weight: 800;
  text-transform: uppercase;
}

.canvas-node-title {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.875rem;
  font-weight: 700;
  color: rgb(17 24 39);
}

.dark .canvas-node-title {
  color: rgb(243 244 246);
}

.canvas-node-status {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  gap: 0.375rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.75rem;
  color: rgb(100 116 139);
}

.canvas-node-status-dot {
  height: 0.5rem;
  width: 0.5rem;
  flex-shrink: 0;
  border-radius: 9999px;
  background: rgb(148 163 184);
}

.canvas-node-preview {
  display: block;
  width: 100%;
  overflow: hidden;
  border-radius: 0.375rem;
  border: 1px solid rgb(226 232 240);
  background: rgb(248 250 252);
}

.canvas-node-preview img {
  display: block;
  height: 3rem;
  width: 100%;
  cursor: zoom-in;
  object-fit: cover;
}

.canvas-result-output {
  display: flex;
  min-height: 7rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  overflow: hidden;
  border: 1px dashed rgb(191 219 254);
  border-radius: 0.375rem;
  padding: 0.75rem;
  color: rgb(100 116 139);
  font-size: 0.75rem;
  text-align: center;
}

.canvas-result-output-image {
  display: block;
  max-height: 11rem;
  width: 100%;
  cursor: zoom-in;
  object-fit: contain;
}

.canvas-reference-upload {
  display: flex;
  min-height: 7rem;
  cursor: pointer;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  overflow: hidden;
  border: 1px dashed rgb(148 163 184);
  border-radius: 0.375rem;
  padding: 0.6rem;
  color: rgb(71 85 105);
  font-size: 0.75rem;
  text-align: center;
}

.canvas-reference-upload:hover {
  border-color: rgb(20 184 166);
  background: rgb(240 253 250);
}

.canvas-reference-upload-busy {
  cursor: wait;
  opacity: 0.65;
}

.canvas-reference-upload img {
  max-height: 6rem;
  max-width: 100%;
  object-fit: contain;
}

.canvas-reference-upload small {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .canvas-reference-upload:hover {
  background: rgb(19 78 74 / 0.25);
}

.dark .canvas-result-output {
  border-color: rgb(30 58 138);
  color: rgb(148 163 184);
}

.canvas-node-result-summary,
.canvas-node-error {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.6875rem;
}

.canvas-node-result-summary {
  color: rgb(5 150 105);
}

.canvas-node-error {
  color: rgb(220 38 38);
}

.dark .canvas-node-status {
  color: rgb(148 163 184);
}

.dark .canvas-node-preview {
  border-color: rgb(55 65 81);
  background: rgb(15 23 42);
}

.canvas-stage-empty {
  position: absolute;
  inset: 0;
  min-height: 18rem;
}

.canvas-node-type-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
}

.canvas-node-type-button {
  display: inline-flex;
  min-height: 2.5rem;
  align-items: center;
  gap: 0.5rem;
  border: 1px solid currentColor;
  border-radius: 0.5rem;
  padding: 0.5rem 0.625rem;
  font-size: 0.8125rem;
  font-weight: 700;
  text-align: left;
}

.canvas-node-editor {
  display: grid;
  gap: 0.625rem;
}

.canvas-dimension-mode {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.25rem;
  padding: 0.25rem;
  border-radius: 0.5rem;
  background: rgb(241 245 249);
}

.canvas-dimension-mode-button {
  min-height: 2rem;
  border: 1px solid transparent;
  border-radius: 0.375rem;
  background: transparent;
  color: rgb(100 116 139);
  font-size: 0.75rem;
  font-weight: 700;
}

.canvas-dimension-mode-button-active {
  border-color: rgb(37 99 235);
  background: rgb(255 255 255);
  color: rgb(30 64 175);
}

.canvas-resolution-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.5rem;
}

.canvas-resolution-button,
.canvas-aspect-button {
  border: 1px solid rgb(226 232 240);
  border-radius: 0.5rem;
  background: rgb(255 255 255);
  color: rgb(71 85 105);
  font-size: 0.8125rem;
  font-weight: 700;
}

.canvas-resolution-button {
  min-height: 2.35rem;
}

.canvas-resolution-button-active,
.canvas-aspect-button-active {
  border-color: rgb(59 130 246);
  background: rgb(239 246 255);
  color: rgb(37 99 235);
}

.canvas-aspect-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0.45rem;
}

.canvas-aspect-button {
  display: flex;
  min-height: 4rem;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 0.3rem;
  font-size: 0.75rem;
}

.canvas-aspect-shape {
  display: block;
  box-sizing: border-box;
  border: 1px solid currentColor;
  border-radius: 0.125rem;
}

.canvas-custom-dimension-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 1.25rem minmax(0, 1fr);
  align-items: end;
  gap: 0.45rem;
}

.canvas-dimension-cross {
  padding-bottom: 0.55rem;
  color: rgb(148 163 184);
  text-align: center;
}

.canvas-dimension-summary {
  margin: -0.15rem 0 0;
  color: rgb(71 85 105);
  font-size: 0.75rem;
  font-weight: 700;
}

.canvas-dimension-summary-error {
  color: rgb(225 29 72);
}

.canvas-field {
  display: grid;
  gap: 0.375rem;
  font-size: 0.75rem;
  font-weight: 700;
  color: rgb(71 85 105);
}

.canvas-field-tight {
  margin-bottom: 0.5rem;
}

.canvas-textarea {
  min-height: 4.75rem;
  resize: vertical;
}

.canvas-node-editor-status {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.75rem;
  color: rgb(100 116 139);
}

.canvas-node-download-button {
  display: inline-flex;
  min-height: 1.875rem;
  margin-left: auto;
  align-items: center;
  gap: 0.3rem;
  border: 1px solid rgb(191 219 254);
  border-radius: 0.375rem;
  background: rgb(239 246 255);
  padding: 0 0.5rem;
  color: rgb(37 99 235);
  font-size: 0.75rem;
  font-weight: 700;
}

.canvas-node-download-button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.canvas-image-preview-overlay {
  position: fixed;
  z-index: 80;
  inset: 0;
  display: grid;
  place-items: center;
  padding: 1.5rem;
  background: rgb(15 23 42 / 0.8);
}

.canvas-image-preview {
  display: flex;
  max-height: calc(100dvh - 3rem);
  width: min(100%, 72rem);
  flex-direction: column;
  overflow: hidden;
  border-radius: 0.5rem;
  background: rgb(255 255 255);
  box-shadow: 0 24px 60px rgb(15 23 42 / 0.35);
}

.canvas-image-preview-header {
  display: flex;
  min-height: 3rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.5rem 0.75rem;
  color: rgb(30 41 59);
  font-size: 0.875rem;
  font-weight: 700;
}

.canvas-image-preview-actions {
  display: flex;
  flex-shrink: 0;
  gap: 0.25rem;
}

.canvas-image-preview > img {
  display: block;
  min-height: 0;
  max-height: calc(100dvh - 6rem);
  width: 100%;
  object-fit: contain;
  background: rgb(15 23 42);
}

.dark .canvas-field,
.dark .canvas-node-editor-status {
  color: rgb(148 163 184);
}

.dark .canvas-node-download-button {
  border-color: rgb(30 64 175);
  background: rgb(30 58 138 / 0.35);
  color: rgb(147 197 253);
}

.dark .canvas-image-preview {
  background: rgb(17 24 39);
}

.dark .canvas-image-preview-header {
  border-bottom-color: rgb(55 65 81);
  color: rgb(226 232 240);
}

.dark .canvas-dimension-mode {
  background: rgb(30 41 59);
}

.dark .canvas-dimension-mode-button-active,
.dark .canvas-resolution-button,
.dark .canvas-aspect-button {
  background: rgb(17 24 39);
}

.dark .canvas-resolution-button,
.dark .canvas-aspect-button {
  border-color: rgb(55 65 81);
  color: rgb(203 213 225);
}

.dark .canvas-resolution-button-active,
.dark .canvas-aspect-button-active {
  border-color: rgb(59 130 246);
  background: rgb(30 58 138 / 0.35);
  color: rgb(147 197 253);
}

.canvas-node-list-dot,
.canvas-run-status {
  height: 0.625rem;
  width: 0.625rem;
  flex-shrink: 0;
  border-radius: 9999px;
}

.canvas-template-entry {
  display: flex;
  align-items: flex-start;
  gap: 0.75rem;
  border-radius: 0.5rem;
  border: 1px dashed rgb(203 213 225);
  padding: 0.75rem;
  color: rgb(20 184 166);
}

.dark .canvas-template-entry {
  border-color: rgb(75 85 99);
  color: rgb(94 234 212);
}

.canvas-kind-text {
  color: rgb(37 99 235);
  background: rgb(239 246 255);
}

.canvas-kind-image {
  color: rgb(5 150 105);
  background: rgb(236 253 245);
}

.canvas-kind-prompt {
  color: rgb(124 58 237);
  background: rgb(245 243 255);
}

.canvas-kind-loop {
  color: rgb(217 119 6);
  background: rgb(255 251 235);
}

.canvas-kind-group {
  color: rgb(71 85 105);
  background: rgb(248 250 252);
}

.canvas-kind-text-to-image {
  color: rgb(219 39 119);
  background: rgb(253 242 248);
}

.canvas-kind-image-to-image {
  color: rgb(14 116 144);
  background: rgb(236 254 255);
}

.canvas-kind-result {
  color: rgb(22 163 74);
  background: rgb(240 253 244);
}

.canvas-run-status-queued {
  background: rgb(59 130 246);
}

.canvas-run-status-idle,
.canvas-node-status-idle {
  background: rgb(148 163 184);
}

.canvas-run-status-running {
  background: rgb(245 158 11);
}

.canvas-run-status-succeeded {
  background: rgb(34 197 94);
}

.canvas-node-status-done {
  background: rgb(34 197 94);
}

.canvas-node-status-queued {
  background: rgb(59 130 246);
}

.canvas-node-status-running {
  background: rgb(245 158 11);
}

.canvas-run-status-failed,
.canvas-run-status-canceled,
.canvas-node-status-failed {
  background: rgb(239 68 68);
}

@media (max-width: 1279px) {
  .canvas-studio {
    grid-template-columns: 280px minmax(0, 1fr);
  }

  .canvas-inspector-panel {
    grid-column: 1 / -1;
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .canvas-inspector-panel .canvas-section {
    border-right: 1px solid rgb(243 244 246);
  }

  .dark .canvas-inspector-panel .canvas-section {
    border-right-color: rgb(55 65 81);
  }
}

@media (max-width: 900px) {
  .canvas-studio {
    grid-template-columns: 1fr;
  }

  .canvas-inspector-panel {
    display: flex;
  }

  .canvas-panel,
  .canvas-workspace {
    min-width: 0;
  }

  .canvas-panel-header,
  .canvas-stage-header {
    align-items: flex-start;
  }

  .canvas-panel-header {
    flex-wrap: wrap;
  }

  .canvas-panel-header .btn {
    width: 100%;
    justify-content: center;
  }

  .canvas-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .canvas-toolbar-actions {
    width: 100%;
    justify-content: stretch;
  }

  .canvas-toolbar-actions .btn {
    flex: 1;
    justify-content: center;
  }

  .canvas-latest-run {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .canvas-stage {
    min-height: min(70dvh, 34rem);
  }

  .canvas-stage-header {
    flex-wrap: wrap;
  }

  .canvas-stage-tools {
    width: 100%;
    justify-content: flex-end;
  }

  .canvas-inspector-panel .canvas-section {
    border-right: 0;
  }
}
</style>
