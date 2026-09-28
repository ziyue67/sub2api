<template>
  <AppLayout>
    <div class="quality-workspace">
      <SmartOpsNav />
      <div class="workspace-heading">
        <div><p class="workspace-eyebrow">{{ t('qualityOps.workspaceLabel') }}</p><h2>{{ t('qualityOps.workspaceTitle') }}</h2><p class="workspace-subtitle">{{ t('qualityOps.workspaceHint') }}</p></div>
        <div class="heading-actions"><span class="updated-label" aria-live="polite">{{ store.updatedAt ? t('qualityOps.updatedAt', { time: clock(store.updatedAt) }) : t('qualityOps.loading') }}</span><button class="btn btn-secondary" :disabled="refreshing" @click="load"><Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" />{{ t('qualityOps.refresh') }}</button><button class="btn btn-primary" @click="newPlan"><Icon name="plus" size="sm" />{{ t('qualityOps.create') }}</button></div>
      </div>
      <div v-if="error || notice" class="workspace-message" :class="error ? 'message-error' : 'message-success'" :role="error ? 'alert' : 'status'">{{ error || notice }}<button :aria-label="t('qualityOps.dismiss')" @click="error = ''; notice = ''"><Icon name="x" size="sm" /></button></div>
      <div class="workspace-summary">
        <div class="summary-cell"><span class="summary-icon"><Icon name="users" size="md" /></span><div><span>{{ t('qualityOps.rulesTotal') }}</span><strong>{{ store.rulesLoaded ? plans.length : '—' }}</strong></div></div>
        <div class="summary-cell"><span class="summary-icon is-teal"><Icon name="play" size="md" /></span><div><span>{{ t('qualityOps.rulesActive') }}</span><strong>{{ store.rulesLoaded ? enabledCount : '—' }}<small v-if="store.rulesLoaded"> / {{ plans.length }}</small></strong></div></div>
        <div class="summary-cell"><span class="summary-icon is-amber"><Icon name="exclamationCircle" size="md" /></span><div><span>{{ t('qualityOps.latestAttention') }}</span><strong>{{ store.operationsLoaded ? attentionCount : '—' }}</strong></div><button v-if="attentionCount" class="summary-link" @click="store.selectedPlanId = null; store.operationFilter = 'attention'">{{ t('qualityOps.view') }}<Icon name="arrowRight" size="xs" /></button></div>
        <div class="summary-cell"><span class="summary-icon"><Icon name="document" size="md" /></span><div><span>{{ t('qualityOps.loadedRounds') }}</span><strong>{{ store.operationsLoaded ? operations.length : '—' }}</strong></div></div>
      </div>
      <div class="workspace-columns">
        <section class="workspace-panel rules-panel" aria-labelledby="quality-rules-heading">
          <header class="panel-heading"><div><h3 id="quality-rules-heading">{{ t('qualityOps.ruleLibrary') }}<span class="count-label">{{ plans.length }}</span></h3><p>{{ t('qualityOps.ruleLibraryHint') }}</p></div></header>
          <div class="rule-search"><Icon name="search" size="sm" /><input v-model="store.search" :aria-label="t('qualityOps.search')" :placeholder="t('qualityOps.searchRule')" /><button v-if="store.search" :aria-label="t('qualityOps.clearSearch')" @click="store.search = ''"><Icon name="x" size="sm" /></button></div>
          <div class="rule-selection-toolbar">
            <label class="rule-check-all"><input type="checkbox" data-testid="quality-select-rules" :checked="allFilteredSelected" :indeterminate="someFilteredSelected && !allFilteredSelected" :disabled="!filteredPlans.length || busy" @change="toggleFilteredSelection" />{{ t('qualityOps.selectFilteredRules') }}</label>
            <span aria-live="polite">{{ t('qualityOps.rulesSelected', { count: selectedRuleIds.length }) }}</span>
            <button v-if="selectedRuleIds.length" :disabled="busy" data-testid="quality-clear-rules" @click="selectedRuleIds = []">{{ t('qualityOps.clearAccountSelection') }}</button>
            <button class="btn btn-primary bulk-edit-button" data-testid="quality-bulk-edit" :disabled="!selectedRuleIds.length || busy || selectedRulesPending" @click="editSelectedRules"><Icon name="edit" size="sm" />{{ t('qualityOps.bulkEdit') }}</button>
          </div>
          <button class="all-accounts" :class="{ selected: store.selectedPlanId === null }" :aria-pressed="store.selectedPlanId === null" @click="store.selectedPlanId = null"><Icon name="users" size="sm" />{{ t('qualityOps.allAccounts') }}<span>{{ plans.length }}</span></button>
          <div class="rules-scroll" data-testid="rules-scroll" :aria-busy="store.rulesLoading">
            <div v-if="store.rulesError" class="panel-error" role="alert">{{ store.rulesError }}<button @click="store.refreshRules(true)">{{ t('qualityOps.retry') }}</button></div>
            <div v-if="!store.rulesLoaded && store.rulesLoading" class="skeleton-stack" role="status" :aria-label="t('qualityOps.loading')"><div v-for="n in 4" :key="n" class="rule-skeleton"><span /><span /><span /></div></div>
            <article v-for="plan in filteredPlans" :key="plan.id" class="rule-card" :class="{ selected: store.selectedPlanId === plan.id, checked: selectedRuleIds.includes(plan.id) }" :data-plan-id="plan.id">
              <div class="rule-card-top"><label class="rule-checkbox"><input v-model="selectedRuleIds" type="checkbox" :value="plan.id" :disabled="busy || !!pending[plan.id]" :aria-label="t('qualityOps.selectRule', { account: name(plan), id: plan.id })" /></label><button class="rule-select" :aria-pressed="store.selectedPlanId === plan.id" @click="store.selectedPlanId = plan.id"><span class="account-avatar">{{ name(plan).slice(0, 1) }}</span><span class="min-w-0"><strong :title="name(plan)">{{ name(plan) }}</strong><span class="rule-meta">#{{ plan.account_id }}<span>·</span>{{ t('qualityOps.rule') }} {{ plan.id }}</span></span></button><button class="state-toggle" :class="plan.enabled ? 'state-enabled' : 'state-paused'" :disabled="busy || !!pending[plan.id]" :title="t(plan.enabled ? 'qualityOps.pause' : 'qualityOps.enable')" @click="toggle(plan)"><span />{{ t(plan.enabled ? 'qualityOps.activeShort' : 'qualityOps.paused') }}</button></div>
              <div class="rule-model"><code>{{ plan.model_id }}</code><span v-if="isProbePlan(plan)" class="probe-tag" data-testid="quality-probe-tag">{{ t('qualityOps.probeTag') }}</span><span v-if="running(plan)" class="running-label">{{ t('qualityOps.running') }}</span></div>
              <div class="rule-target" :title="planGroups(plan)"><Icon name="users" size="xs" /><span>{{ planGroups(plan) }}</span></div>
              <div class="rule-schedule"><span>{{ t('qualityOps.nextRun') }}</span><time :datetime="plan.next_run_at || undefined">{{ plan.enabled ? date(plan.next_run_at) : '—' }}</time></div>
              <div v-if="!isProbePlan(plan) && !plan.pelican_config?.quality?.judge" class="rule-warning">{{ t('qualityOps.configureJudge') }}</div>
              <footer class="rule-actions"><button @click="history(plan)"><Icon name="document" size="xs" />{{ t('qualityOps.historyShort') }}</button><button :disabled="!!pending[plan.id]" @click="edit(plan)">{{ t('qualityOps.editShort') }}</button><button :disabled="!!pending[plan.id] || !plan.enabled || running(plan)" @click="run(plan)"><Icon name="play" size="xs" />{{ pending[plan.id] === 'run' ? t('qualityOps.submitting') : t('qualityOps.runShort') }}</button></footer>
            </article>
            <div v-if="store.rulesLoaded && !filteredPlans.length" class="panel-empty"><Icon name="inbox" size="lg" /><p>{{ plans.length ? t('qualityOps.noMatchingRules') : t('qualityOps.empty') }}</p><button v-if="!plans.length" class="btn btn-secondary" @click="newPlan">{{ t('qualityOps.create') }}</button></div>
          </div>
        </section>
        <section class="workspace-panel operations-panel" aria-labelledby="quality-operations-heading">
          <header class="panel-heading"><div><h3 id="quality-operations-heading">{{ t('qualityOps.operations') }}</h3><p>{{ t('qualityOps.operationsSubtitle') }}</p></div><button class="icon-button" :disabled="store.operationsLoading" :aria-label="t('qualityOps.refreshOperations')" @click="refreshOperations"><Icon name="refresh" size="sm" :class="{ 'animate-spin': store.operationsLoading }" /></button></header>
          <div class="operations-toolbar"><div class="scope-label"><span class="scope-dot" />{{ selectedPlan ? name(selectedPlan) : t('qualityOps.allAccounts') }}<button v-if="selectedPlan" :aria-label="t('qualityOps.clearScope')" @click="store.selectedPlanId = null"><Icon name="x" size="xs" /></button></div><select v-model="store.operationFilter" :aria-label="t('qualityOps.filterRecords')"><option value="all">{{ t('qualityOps.allOutcomes') }}</option><option value="attention">{{ t('qualityOps.needsAttention') }}</option><option value="passed">{{ t('qualityOps.passedRounds') }}</option><option value="failed">{{ t('qualityOps.otherRounds') }}</option></select></div>
          <div v-if="store.operationsError" role="alert" class="panel-error">{{ store.operationsError }}<button @click="refreshOperations">{{ t('qualityOps.retry') }}</button></div>
          <div class="operations-scroll" data-testid="operations-scroll" :aria-busy="store.operationsLoading">
            <table class="operations-table"><thead><tr><th>{{ t('qualityOps.time') }}</th><th>{{ t('qualityOps.accounts') }}</th><th>{{ t('qualityOps.testResult') }}</th><th>{{ t('qualityOps.accountAction') }}</th><th><span class="sr-only">{{ t('qualityOps.details') }}</span></th></tr></thead><tbody>
              <template v-if="!store.operationsLoaded && store.operationsLoading"><tr v-for="n in 6" :key="`loading-${n}`" class="loading-row"><td v-for="c in 5" :key="c"><span class="cell-skeleton" /></td></tr></template>
              <tr v-for="operation in filteredOperations" :key="operation.id" :class="{ 'selected-row': detailOperation?.id === operation.id && !!historyPlan }" :data-operation-id="operation.id">
                <td class="time-cell" :data-label="t('qualityOps.time')"><strong>{{ clock(operation.started_at) }}</strong><span>{{ day(operation.started_at) }}</span></td>
                <td class="account-cell"><button :title="operation.account_name" @click="store.selectedPlanId = operation.plan_id"><strong>{{ operation.account_name || `#${operation.account_id}` }}</strong></button><span>{{ t('qualityOps.rule') }} {{ operation.plan_id }}<span class="mx-1">·</span>#{{ operation.account_id }}</span></td>
                <td :data-label="t('qualityOps.testResult')"><span class="test-count" :class="allPassed(operation) ? 'test-passed' : 'test-other'"><Icon :name="allPassed(operation) ? 'checkCircle' : 'exclamationCircle'" size="xs" />{{ operation.passed_count }} / {{ operation.total_count }}</span><span class="cell-secondary">{{ t(allPassed(operation) ? 'qualityOps.roundPassed' : 'qualityOps.roundNotPassed') }}</span></td>
                <td class="action-cell" :data-label="t('qualityOps.accountAction')"><button class="outcome-badge" :class="tone(operation.quality_action)" @click="operationDetails(operation)"><span />{{ operationLabel(operation) }}</button><span class="cell-secondary" :title="operationGroups(operation)">{{ operationGroups(operation) }}</span></td>
                <td class="detail-cell"><button class="detail-button" :aria-label="t('qualityOps.openRound', { account: operation.account_name, time: date(operation.started_at) })" @click="operationDetails(operation)"><span>{{ t('qualityOps.details') }}</span><Icon name="arrowRight" size="sm" /></button></td>
              </tr>
            </tbody></table>
            <div v-if="store.operationsLoaded && !filteredOperations.length" class="panel-empty"><Icon name="document" size="lg" /><h4>{{ t('qualityOps.noResults') }}</h4><p>{{ t('qualityOps.filteredEmptyHint') }}</p></div>
          </div>
          <footer class="operations-footer"><span>{{ t('qualityOps.showingLoaded', { shown: filteredOperations.length, loaded: operations.length }) }}</span><button v-if="store.cursor" :disabled="store.moreLoading || store.operationsLoading" @click="moreOperations">{{ store.moreLoading ? t('qualityOps.loading') : t('qualityOps.loadMore') }}<Icon name="arrowDown" size="xs" /></button><span v-else>{{ t('qualityOps.loadedEnd') }}</span></footer>
        </section>
      </div>
    </div>

    <!-- Existing rule editor moves into a focused drawer instead of shifting both lists. -->
    <BaseDialog :show="showForm" :title="bulkEditing ? t('qualityOps.bulkEditTitle', { count: bulkRuleIds.length }) : editing ? t('qualityOps.edit') : t('qualityOps.create')" placement="right" width="wide" :close-on-escape="!busy && !deleteTarget && !discardPrompt" @close="closeForm">
      <p v-if="error" role="alert" class="editor-error">{{ error }}</p>
      <p v-if="store.groupsError" role="alert" class="editor-error">{{ store.groupsError }} <button class="underline" @click="store.refreshGroups">{{ t('qualityOps.retry') }}</button></p>
            <form id="quality-rule-form" class="quality-editor space-y-5" @submit.prevent="save"><fieldset :disabled="busy" class="space-y-5">

        <div v-if="bulkEditing" class="bulk-editor-intro">
          <p>{{ t('qualityOps.bulkEditHint') }}</p>
          <details><summary>{{ t('qualityOps.rulesSelected', { count: bulkRuleIds.length }) }}</summary><ul><li v-for="id in bulkRuleIds" :key="id">{{ name(plans.find(plan => plan.id === id) || { id } as ScheduledTestPlan) }} · {{ t('qualityOps.rule') }} {{ id }}</li></ul></details>
          <div class="bulk-fields"><label v-for="field in qualityRuleFields" :key="field"><input v-model="bulkFields" type="checkbox" :value="field" :data-testid="`quality-bulk-field-${field}`" />{{ t(`qualityOps.bulkFields.${field}`) }}</label></div>
          <p v-if="bulkProgress" role="status">{{ bulkProgress }}</p>
        </div>
        <div v-if="!editing && !bulkEditing" class="space-y-3">
          <label for="quality-account-search" class="block text-sm font-medium">{{ t('qualityOps.accounts') }}</label>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="space-y-1 text-sm"><span>{{ t('qualityOps.accountGroup') }}</span>
              <select v-model="accountGroup" class="input" data-testid="quality-account-group" @change="searchAccounts(1)">
                <option value="">{{ t('qualityOps.allAccountGroups') }}</option>
                <option value="ungrouped">{{ t('qualityOps.ungroupedAccounts') }}</option>
                <option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ group.name }} #{{ group.id }}</option>
              </select>
            </label>
            <label class="space-y-1 text-sm"><span>{{ t('qualityOps.accountType') }}</span>
              <select v-model="accountType" class="input" data-testid="quality-account-type" @change="searchAccounts(1)">
                <option value="">{{ t('qualityOps.allAccountTypes') }}</option>
                <option value="oauth">{{ t('qualityOps.oauthAccounts') }}</option>
                <option value="apikey">{{ t('qualityOps.apiKeyAccounts') }}</option>
              </select>
            </label>
          </div>
          <div class="flex gap-2"><input id="quality-account-search" v-model="search" class="input min-w-0" :placeholder="t('qualityOps.search')" @keydown.enter.prevent="searchAccounts(1)" /><button type="button" class="btn btn-secondary shrink-0 whitespace-nowrap" @click="searchAccounts(1)">{{ t('qualityOps.search') }}</button></div>
          <div class="flex flex-wrap items-center gap-3 text-sm">
            <button type="button" class="text-primary-600 disabled:opacity-50" data-testid="quality-select-page" :disabled="accountsLoading || selectingAccounts || !selectableAccounts.length" @click="selectCurrentPage">{{ t('qualityOps.selectAccountPage') }}</button>
            <button type="button" class="text-primary-600 disabled:opacity-50" data-testid="quality-select-all" :disabled="accountsLoading || selectingAccounts || !accounts.length" @click="selectMatchingAccounts">{{ t(selectingAccounts ? 'qualityOps.selectingAccounts' : 'qualityOps.selectMatchingAccounts') }}</button>
            <button type="button" class="text-gray-500 disabled:opacity-50" data-testid="quality-clear-selection" :disabled="!selectedAccounts.length && !selectingAccounts" @click="clearAccountSelection">{{ t('qualityOps.clearAccountSelection') }}</button>
          </div>
          <p class="text-xs text-gray-500">{{ t('qualityOps.accountSelectionHint') }}</p>
          <p v-if="accountsError" role="alert" class="text-sm text-red-600">{{ accountsError }}</p>
          <p v-if="accountsLoading" role="status" class="text-sm text-gray-500">{{ t('qualityOps.loading') }}</p>
          <div class="grid max-h-52 gap-2 overflow-auto rounded border p-3 sm:grid-cols-2 dark:border-dark-600" :aria-busy="accountsLoading || selectingAccounts">
            <label v-for="account in accounts" :key="account.id" class="flex items-center gap-2 text-sm"><input v-model="selectedAccounts" type="checkbox" :value="account.id" :disabled="selectingAccounts || existingAccountIds.has(account.id)" /><span class="min-w-0 break-all">{{ account.name }} <span class="text-gray-500">#{{ account.id }}</span><span v-if="existingAccountIds.has(account.id)" class="ml-1 text-xs text-gray-500">{{ t('qualityOps.accountHasRule') }}</span></span></label>
            <p v-if="!accountsLoading && !accounts.length" class="text-sm text-gray-500 sm:col-span-2">{{ t('qualityOps.noMatchingAccounts') }}</p>
          </div>
          <div class="flex flex-wrap items-center gap-3 text-sm"><button type="button" :aria-label="t('qualityOps.previousAccountPage')" :disabled="accountsLoading || selectingAccounts || accountPage <= 1" @click="searchAccounts(accountPage - 1)">←</button><span>{{ accountPage }} / {{ accountPages }}</span><button type="button" :aria-label="t('qualityOps.nextAccountPage')" :disabled="accountsLoading || selectingAccounts || accountPage >= accountPages" @click="searchAccounts(accountPage + 1)">→</button><span aria-live="polite">{{ t('qualityOps.selected', { count: selectedAccounts.length }) }}</span></div>
        </div>
        <div v-if="editsField('test')" class="space-y-2">
          <label class="block space-y-1"><span>{{ t('qualityOps.questionKind') }}</span><select v-model="form.pelican_config.question_kind" class="input" data-testid="quality-question-kind" @change="selectQuestionKind"><option value="candy">{{ t('qualityOps.questionCandy') }}</option><option :value="STATE_PROBE_QUESTION">{{ t('qualityOps.questionStateProbe') }}</option></select></label>
          <p v-if="isProbe" class="text-sm text-gray-500" data-testid="quality-probe-hint">{{ t('qualityOps.probeHint') }}</p>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <label v-if="editsField('model')" class="space-y-1"><span>{{ t('qualityOps.model') }}</span><input v-model.trim="form.model_id" required maxlength="100" class="input" placeholder="gpt-6-astra" /></label>
          <label v-if="editsField('schedule')" class="space-y-1"><span>{{ t('qualityOps.cron') }}</span><input v-model.trim="form.cron_expression" required class="input" placeholder="*/30 * * * *" /></label>
          <template v-if="editsField('test') && !isProbe">
            <label class="space-y-1"><span>{{ t('qualityOps.effort') }}</span><select v-model="form.pelican_config.reasoning_effort" class="input"><option v-for="effort in ['minimal', 'low', 'medium', 'high', 'xhigh']" :key="effort">{{ effort }}</option></select></label>
            <label class="space-y-1"><span>{{ t('qualityOps.parallel') }}</span><input v-model.number="form.pelican_config.parallel_count" type="number" min="1" max="8" required class="input" /></label>
          </template>
        </div>
        <template v-if="editsField('test') && !isProbe">
        <div><div class="mb-2 flex items-center justify-between"><label for="quality-prompt">{{ t('qualityOps.prompt') }}</label><button type="button" class="text-sm text-primary-600" @click="useCandy">{{ t('qualityOps.candy') }}</button></div><textarea id="quality-prompt" v-model="form.pelican_config.prompt" required maxlength="32000" rows="5" class="input text-sm" /></div>
        <label class="block space-y-1"><span>{{ t('qualityOps.answer') }}</span><input v-model="form.pelican_config.quality.expected_answer" required maxlength="4000" class="input" /></label>
        <fieldset class="space-y-3 rounded-lg border p-4 dark:border-dark-600">
          <legend class="px-2 font-medium">{{ t('qualityOps.judgeTitle') }}</legend>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="space-y-1"><span>{{ t('qualityOps.judgeGroup') }}</span>
              <select v-model.number="form.pelican_config.quality.judge.group_id" required class="input" @change="loadJudgeModels">
                <option disabled :value="0">{{ t('qualityOps.selectJudgeGroup') }}</option>
                <option v-for="group in groups.filter(g => g.status === 'active')" :key="group.id" :value="group.id">{{ group.name }} #{{ group.id }}</option>
              </select>
            </label>
            <label class="space-y-1"><span>{{ t('qualityOps.judgeModel') }}</span>
              <input v-model.trim="form.pelican_config.quality.judge.model_id" list="quality-judge-models" required maxlength="100" class="input" :placeholder="t('qualityOps.selectJudgeModel')" />
              <datalist id="quality-judge-models"><option v-for="model in judgeModels" :key="model" :value="model" /></datalist>
            </label>
          </div>
          <label class="block space-y-1"><span>{{ t('qualityOps.judgePrompt') }}</span><textarea v-model="form.pelican_config.quality.judge.prompt" required maxlength="16000" rows="3" class="input text-sm" /></label>
          <p class="text-sm text-gray-500">{{ t('qualityOps.grading') }}</p>
        </fieldset>
        </template>
        <fieldset v-if="editsField('action')" class="space-y-3 rounded-lg border p-4 dark:border-dark-600">
          <legend class="px-2 font-medium">{{ t(isProbe ? 'qualityOps.probeFailureAction' : 'qualityOps.failureAction') }}</legend>
          <label class="flex items-center gap-2"><input v-model="form.pelican_config.quality.action" type="radio" value="remove_groups" />{{ t('qualityOps.removeGroups') }}</label>
          <div v-if="form.pelican_config.quality.action === 'remove_groups'" class="grid max-h-40 gap-2 overflow-auto pl-6 sm:grid-cols-2">
            <label v-for="group in groups" :key="group.id" class="flex items-center gap-2 text-sm"><input v-model="form.pelican_config.quality.remove_group_ids" type="checkbox" :value="group.id" />{{ group.name }} #{{ group.id }}</label>
          </div>
          <label class="flex items-center gap-2"><input v-model="form.pelican_config.quality.action" type="radio" value="disable_scheduling" />{{ t('qualityOps.disableScheduling') }}</label>
          <template v-if="isProbe">
            <label class="flex items-center gap-2"><input v-model="form.pelican_config.quality.action" type="radio" value="enable_bps" data-testid="quality-action-enable-bps" />{{ t('qualityOps.enableBPS') }}</label>
            <QualityBPSSettings v-if="form.pelican_config.quality.action === 'enable_bps'" v-model:bps="form.pelican_config.quality.bps" v-model:auto-restore="form.pelican_config.quality.auto_restore" class="pl-6" :target-groups="bpsTargetGroups" :show-auto-restore="editsField('restore')" />
          </template>
        </fieldset>
        <template v-if="editsField('restore') && !bpsSettingsShown">
          <QualityBPSRestoreOptions v-if="form.pelican_config.quality.action === 'enable_bps'" v-model:bps="form.pelican_config.quality.bps" v-model:auto-restore="form.pelican_config.quality.auto_restore" class="text-sm" always-show-hold />
          <template v-else><label class="flex items-center gap-2"><input v-model="form.pelican_config.quality.auto_restore" type="checkbox" data-testid="quality-auto-restore" />{{ t('qualityOps.autoRestore') }}</label><p class="text-sm text-gray-500">{{ t('qualityOps.restoreHelp') }}</p></template>
        </template>
        <label v-if="editsField('enabled')" class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" />{{ t('qualityOps.enabled') }}</label>
      </fieldset></form>
      <template #footer><div class="editor-footer"><button v-if="editing" type="button" class="delete-rule" :disabled="busy" @click="deleteTarget = plans.find(p => p.id === editing) || null">{{ t('qualityOps.delete') }}</button><span class="flex-1" /><button class="btn btn-secondary" :disabled="busy" @click="closeForm">{{ t('qualityOps.cancel') }}</button><button form="quality-rule-form" type="submit" class="btn btn-primary" :disabled="busy || selectingAccounts || (bulkEditing ? !bulkFields.length || !bulkRuleIds.length : !editing && !selectedAccounts.length)">{{ busy ? t('qualityOps.saving') : bulkEditing ? t('qualityOps.applyToRules', { count: bulkRuleIds.length }) : t('qualityOps.save') }}</button></div></template>
    </BaseDialog>
    <BaseDialog :show="!!historyPlan" :title="detailOperation ? t('qualityOps.roundDetail') : t('qualityOps.history')" placement="right" width="extra-wide" close-on-click-outside @close="closeDetails">
      <template v-if="historyPlan">
        <div class="detail-heading"><span class="account-avatar">{{ detailAccountName.slice(0, 1) }}</span><div><h3>{{ detailAccountName }}</h3><p>{{ t('qualityOps.rule') }} {{ historyPlan.id }}<span class="mx-2">·</span>{{ detailOperation ? date(detailOperation.started_at) : t('qualityOps.historyHelp') }}</p></div><a class="account-management-link" href="/admin/accounts">{{ t('qualityOps.manageAccount') }}<Icon name="externalLink" size="xs" /></a></div>
        <div v-if="detailOperation" class="round-overview"><div><span>{{ t('qualityOps.testResult') }}</span><strong :class="allPassed(detailOperation) ? 'text-emerald-600' : 'text-rose-600'">{{ detailOperation.passed_count }} / {{ detailOperation.total_count }} {{ t('qualityOps.passed') }}</strong></div><div><span>{{ t('qualityOps.accountAction') }}</span><strong>{{ operationLabel(detailOperation) }}</strong></div></div>
        <aside v-if="detailAction === 'restore_conflict'" class="conflict-explanation" role="note"><Icon name="exclamationTriangle" size="md" /><div v-if="detailBPS"><h4>{{ t('qualityOps.conflictTitle') }}</h4><p>{{ t('qualityOps.bpsConflictExplanation') }}</p><details class="conflict-causes"><summary>{{ t('qualityOps.conflictChecks') }}</summary><ul><li>{{ t('qualityOps.bpsConflictOptions') }}</li><li>{{ t('qualityOps.bpsConflict403') }}</li><li>{{ t('qualityOps.bpsConflictAvailability') }}</li></ul></details><p class="conflict-limit">{{ t('qualityOps.conflictUnknown') }}</p><strong>{{ t('qualityOps.bpsConflictNextStep') }}</strong></div>
          <div v-else><h4>{{ t('qualityOps.conflictTitle') }}</h4><p>{{ t('qualityOps.conflictExplanation') }}</p><details class="conflict-causes"><summary>{{ t('qualityOps.conflictChecks') }}</summary><ul><li>{{ t('qualityOps.conflictAccount') }}</li><li>{{ t('qualityOps.conflictMembership') }}</li><li>{{ t('qualityOps.conflictAvailability') }}</li></ul></details><p class="conflict-limit">{{ t('qualityOps.conflictUnknown') }}</p><strong>{{ t('qualityOps.conflictNextStep') }}</strong></div></aside>
        <div v-else-if="detailAction" class="action-explanation"><Icon name="infoCircle" size="sm" /><p>{{ actionExplanation(detailAction) }}</p></div>
        <dl v-if="detailOperation?.pelican_config?.quality" class="detail-policy"><div><dt>{{ t(detailBPS ? 'qualityOps.ruleAction' : 'qualityOps.targetGroups') }}</dt><dd>{{ operationGroups(detailOperation) }}</dd></div><div><dt>{{ t(detailBPS ? 'qualityOps.bpsAutoDisableShort' : 'qualityOps.autoRestoreShort') }}</dt><dd>{{ t(detailOperation.pelican_config.quality.auto_restore ? 'qualityOps.on' : 'qualityOps.off') }}</dd></div></dl>
        <div v-if="detailsError" class="panel-error" role="alert">{{ detailsError }}<button @click="retryDetails">{{ t('qualityOps.retry') }}</button></div>
        <div v-if="detailsLoading" class="detail-loading" role="status"><span class="cell-skeleton" /><span class="cell-skeleton" />{{ t('qualityOps.loading') }}</div>
        <div v-else class="detail-grid">
          <nav class="result-navigation" :aria-label="t('qualityOps.probes')"><p>{{ t('qualityOps.probes') }}<span>{{ results.length }}</span></p><button v-for="(result, index) in results" :key="result.id" :class="{ selected: selectedResultId === result.id }" :aria-pressed="selectedResultId === result.id" @click="selectResult(result)"><span>{{ detailOperation ? t('qualityOps.probeNumber', { n: index + 1 }) : date(result.started_at) }}</span><span :class="resultTextClass(result)">{{ resultLabel(result) }}</span></button></nav>
          <section class="answer-detail" :aria-busy="answerLoading">
            <div v-if="answerLoading" class="detail-loading" role="status"><span class="cell-skeleton" /><span class="cell-skeleton" />{{ t('qualityOps.loadingAnswer') }}</div>
            <template v-else-if="selectedResult"><header class="answer-heading"><h4>{{ t(isProbeResult(selectedResult) ? 'qualityOps.probeVerdictTitle' : 'qualityOps.answerAndVerdict') }}</h4><span class="outcome-badge" :class="resultTone(selectedResult)" data-testid="quality-result-badge">{{ resultLabel(selectedResult) }}</span></header>
              <div v-if="!isProbeResult(selectedResult)" class="answer-reference"><span>{{ t('qualityOps.answer') }}</span><p>{{ selectedResult.pelican_config?.quality?.expected_answer || detailOperation?.pelican_config?.quality?.expected_answer || '—' }}</p></div>
              <div class="response-heading">{{ t(isProbeResult(selectedResult) ? 'qualityOps.probeDetail' : 'qualityOps.actualAnswer') }}<span v-if="selectedResult.latency_ms">{{ (selectedResult.latency_ms / 1000).toFixed(1) }}s</span></div>
              <pre class="response-content">{{ selectedResult.response_text || selectedResult.error_message || t('qualityOps.noAnswer') }}</pre>
              <div v-if="selectedResult.quality_judgment && !isProbeResult(selectedResult)" class="judge-reason"><h5>{{ t('qualityOps.judgeReason') }}</h5><p>{{ selectedResult.quality_judgment.reason || '—' }}</p><span>{{ selectedResult.quality_judgment.model_id || '—' }} · {{ groupNames[selectedResult.quality_judgment.group_id || 0] || selectedResult.quality_judgment.group_id || '—' }}</span></div>
            </template><div v-else-if="!detailsError" class="panel-empty">{{ t('qualityOps.noResults') }}</div>
          </section>
        </div>
      </template>
      <template #footer><div class="detail-footer"><button class="btn btn-secondary" :disabled="operationIndex <= 0 || !detailOperation" @click="navigateOperation(-1)"><Icon name="arrowLeft" size="sm" />{{ t('qualityOps.previousRound') }}</button><span>{{ detailOperation ? `${operationIndex + 1} / ${filteredOperations.length}` : t('qualityOps.history') }}</span><button class="btn btn-secondary" :disabled="!detailOperation || operationIndex < 0 || operationIndex >= filteredOperations.length - 1" @click="navigateOperation(1)">{{ t('qualityOps.nextRound') }}<Icon name="arrowRight" size="sm" /></button></div></template>
    </BaseDialog>
    <BaseDialog :show="!!deleteTarget" :title="t('qualityOps.delete')" width="narrow" :close-on-escape="!deleting" @close="!deleting && (deleteTarget = null)"><p class="text-sm text-gray-600 dark:text-gray-300">{{ t('qualityOps.deleteConfirm') }}</p><p class="mt-3 font-medium">{{ deleteTarget ? name(deleteTarget) : '' }}</p><template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="deleting" @click="deleteTarget = null">{{ t('qualityOps.cancel') }}</button><button class="btn bg-red-600 text-white hover:bg-red-700" :disabled="deleting" @click="confirmDelete">{{ t('qualityOps.delete') }}</button></div></template></BaseDialog>
    <BaseDialog :show="discardPrompt" :title="t('qualityOps.unsavedTitle')" width="narrow" @close="discardPrompt = false"><p>{{ t('qualityOps.unsavedHint') }}</p><template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" @click="discardPrompt = false">{{ t('qualityOps.keepEditing') }}</button><button class="btn btn-primary" @click="discardPrompt = false; showForm = false">{{ t('qualityOps.discard') }}</button></div></template></BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import Icon from '@/components/icons/Icon.vue'
import QualityBPSSettings from '@/components/admin/operations/QualityBPSSettings.vue'
import QualityBPSRestoreOptions from '@/components/admin/operations/QualityBPSRestoreOptions.vue'
import { useAccountQualityStore } from '@/stores/accountQuality'
import { useAuthStore } from '@/stores/auth'
import { runQualityPlan, type QualityOperation } from '@/api/admin/accountQuality'
import scheduledTests from '@/api/admin/scheduledTests'
import * as accountsAPI from '@/api/admin/accounts'
import * as groupsAPI from '@/api/admin/groups'
import { CANDY_PROMPT, STATE_PROBE_QUESTION, stateProbeVerdict, type StateProbeVerdict } from '@/utils/intelligenceTest'
import { buildQualityRulePatch, defaultQualityBPS, qualityBPSError, qualityBPSForm, qualityBPSPayload, qualityRuleFields, type QualityRuleField } from '@/utils/qualityRulePatch'
import type { AccountListItem, QualityBPSPolicy, QualityPolicy, ScheduledTestPlan, ScheduledTestResult } from '@/types'

const { t, te } = useI18n()
const store = useAccountQualityStore(), auth = useAuthStore()
const { plans, groups, operations } = storeToRefs(store)
const groupNames = computed(() => Object.fromEntries(groups.value.map(g => [g.id, g.name])))
const accountNames = computed(() => {
  const names: Record<number, string> = {}
  for (const op of operations.value) if (op.account_name) names[op.account_id] = op.account_name
  for (const p of plans.value) if (p.account_name) names[p.account_id] = p.account_name
  for (const account of accounts.value) names[account.id] = account.name
  return names
})
const accounts = ref<AccountListItem[]>([]), accountsLoading = ref(false)
const search = ref(''), accountPage = ref(1), accountPages = ref(1)
const accountGroup = ref(''), accountType = ref(''), accountsError = ref(''), selectingAccounts = ref(false)
const existingAccountIds = computed(() => new Set(plans.value.map(plan => plan.account_id)))
const selectableAccounts = computed(() => accounts.value.filter(account => !existingAccountIds.value.has(account.id)))
let accountSelectionRequest = 0
const accountFilters = () => ({ search: search.value.trim(), group: accountGroup.value || undefined, type: accountType.value || undefined, lite: 'true', sort_by: 'id', sort_order: 'asc' as const })
function invalidateAccountRequests() {
  accountRequest++; accountSelectionRequest++
  accountsLoading.value = selectingAccounts.value = false
  accounts.value = []; accountPage.value = accountPages.value = 1; accountsError.value = ''
}
watch([search, accountGroup, accountType], invalidateAccountRequests, { flush: 'sync' })
function showAccountPicker() { return showForm.value && !editing.value && !bulkEditing.value }

const selectedAccounts = ref<number[]>([]), editing = ref<number | null>(null)
const selectedRuleIds = ref<number[]>([]), bulkRuleIds = ref<number[]>([])
const bulkEditing = ref(false), bulkFields = ref<QualityRuleField[]>([]), bulkProgress = ref('')
const busy = ref(false), error = ref(''), notice = ref(''), showForm = ref(false)
watch(showAccountPicker, (show) => { if (!show) invalidateAccountRequests() }, { flush: 'sync' })
const pending = ref<Record<number, string>>({}), deleteTarget = ref<ScheduledTestPlan | null>(null), deleting = ref(false)
const discardPrompt = ref(false), initialForm = ref('')
const judgeModels = ref<string[]>([])
let judgeModelsRequest = 0, accountRequest = 0, detailRequest = 0, answerRequest = 0
let alive = true, poll: ReturnType<typeof setInterval> | undefined
const historyPlan = ref<ScheduledTestPlan | null>(null), detailOperation = ref<QualityOperation | null>(null)
const results = ref<ScheduledTestResult[]>([]), selectedResultId = ref<number | null>(null)
const selectedResult = ref<ScheduledTestResult | null>(null)
const detailsLoading = ref(false), answerLoading = ref(false), detailsError = ref('')
const loadedAnswers = new Map<number, ScheduledTestResult>()
const identity = () => auth.user ? `${auth.user.id}:${auth.user.role}` : ''
const refreshing = computed(() => store.rulesLoading || store.operationsLoading)
const enabledCount = computed(() => plans.value.filter(p => p.enabled).length)
// 开 BPS 被 403 记录挡住或账号不支持 BPS 时，规则无法自动处理，也列为待处理。
const attentionActions = new Set(['restore_conflict', 'action_error', 'bps_blocked_403', 'bps_unsupported'])
const attentionCount = computed(() => {
  const latest = new Map<number, QualityOperation>()
  for (const operation of operations.value) if (!latest.has(operation.plan_id)) latest.set(operation.plan_id, operation)
  return [...latest.values()].filter(op => attentionActions.has(op.quality_action || '')).length
})
const selectedPlan = computed(() => plans.value.find(p => p.id === store.selectedPlanId))
const filteredPlans = computed(() => {
  const query = store.search.trim().toLowerCase()
  return plans.value.filter(p => !query || `${name(p)} ${p.account_id} ${p.id} ${p.model_id}`.toLowerCase().includes(query))
})
const allFilteredSelected = computed(() => filteredPlans.value.length > 0 && filteredPlans.value.every(plan => selectedRuleIds.value.includes(plan.id)))
const someFilteredSelected = computed(() => filteredPlans.value.some(plan => selectedRuleIds.value.includes(plan.id)))
const selectedRulesPending = computed(() => selectedRuleIds.value.some(id => !!pending.value[id]))
watch(plans, (current) => {
  const ids = new Set(current.map(plan => plan.id))
  selectedRuleIds.value = selectedRuleIds.value.filter(id => ids.has(id))
})
function toggleFilteredSelection() {
  if (busy.value) return
  const ids = new Set(filteredPlans.value.map(plan => plan.id))
  selectedRuleIds.value = allFilteredSelected.value
    ? selectedRuleIds.value.filter(id => !ids.has(id))
    : [...new Set([...selectedRuleIds.value, ...ids])]
}
function editsField(field: QualityRuleField) { return !bulkEditing.value || bulkFields.value.includes(field) }
const filteredOperations = computed(() => operations.value.filter(op => {
  if (store.selectedPlanId !== null && op.plan_id !== store.selectedPlanId) return false
  if (store.operationFilter === 'attention') return attentionActions.has(op.quality_action || '')
  if (store.operationFilter === 'passed') return allPassed(op)
  if (store.operationFilter === 'failed') return !allPassed(op)
  return true
}))
const operationIndex = computed(() => filteredOperations.value.findIndex(op => op.id === detailOperation.value?.id))
const detailAccountName = computed(() => detailOperation.value?.account_name || (historyPlan.value ? name(historyPlan.value) : ''))
const detailAction = computed(() => detailOperation.value?.quality_action || selectedResult.value?.quality_action)
const detailBPS = computed(() => (detailOperation.value ?? selectedResult.value)?.pelican_config?.quality?.action === 'enable_bps')
function name(plan: ScheduledTestPlan) { return plan.account_name || accountNames.value[plan.account_id] || `#${plan.account_id ?? plan.id}` }
function date(value: string | null | undefined) { return value ? new Date(value).toLocaleString(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—' }
function clock(value: string | number | undefined) { return value ? new Date(value).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }) : '—' }
function day(value: string | undefined) { return value ? new Date(value).toLocaleDateString() : '—' }
function running(plan: ScheduledTestPlan) { return !!plan.running_until && Date.parse(plan.running_until) > Date.now() }
function allPassed(op: QualityOperation) { return op.total_count > 0 && op.passed_count === op.total_count }
function bpsTrigger(policy?: QualityBPSPolicy) {
  const parts = [
    policy?.failure_threshold ? t('qualityOps.bpsTriggerCount', { n: policy.failure_threshold }) : '',
    policy?.usage_percent ? t('qualityOps.bpsTriggerUsage', { n: policy.usage_percent }) : '',
  ].filter(Boolean)
  return parts.join(policy?.require_all ? ' + ' : ' / ')
}
function policyTarget(quality?: QualityPolicy) {
  if (quality?.action === 'remove_groups') return quality.remove_group_ids.map(id => groupNames.value[id] || `#${id}`).join(' / ')
  if (quality?.action !== 'enable_bps') return t('qualityOps.disableSchedulingShort')
  const trigger = bpsTrigger(quality.bps)
  return trigger ? `${t('qualityOps.enableBPSShort')}（${trigger}）` : t('qualityOps.enableBPSShort')
}
function planGroups(plan: ScheduledTestPlan) { return policyTarget(plan.pelican_config?.quality) }
function operationGroups(op: QualityOperation) { return policyTarget(op.pelican_config?.quality) }
// 「降智开 BPS」未开启时后端记为 failure_counted:N/M，已开启但满血次数未到关闭次数时记为 restore_counted:N/M，
// 展示时拆出进度；「同时满足」下次数已够、只差用量时 failure_counted 的 N 会 ≥ M。
function countedOutcome(action?: string) {
  const match = /^(failure_counted|restore_counted):(\d+)\/(\d+)$/.exec(action || '')
  return match ? { kind: match[1], n: Number(match[2]), m: Number(match[3]) } : null
}
function actionKey(action?: string) { return countedOutcome(action)?.kind || action || '' }
function actionLabel(action?: string) {
  const counted = countedOutcome(action)
  if (counted?.kind === 'restore_counted') return t('qualityOps.outcomes.restore_counted_n', counted)
  if (counted) return t(counted.n >= counted.m ? 'qualityOps.outcomes.failure_counted_usage' : 'qualityOps.outcomes.failure_counted_n', counted)
  const key = `qualityOps.outcomes.${action}`; return action && te(key) ? t(key) : '—'
}
function operationLabel(op: QualityOperation) {
  const policy = op.pelican_config?.quality?.action, bpsKey = `qualityOps.bpsOutcomes.${actionKey(op.quality_action)}`
  if (op.quality_action === 'restored' && policy === 'remove_groups') return t('qualityOps.groupsRestored')
  if (policy === 'enable_bps' && op.quality_action && te(bpsKey)) return t(bpsKey)
  return actionLabel(op.quality_action)
}
function tone(action?: string) {
  const key = actionKey(action)
  if (attentionActions.has(key)) return 'tone-warning'
  if (['restored', 'passed', 'bps_enabled', 'bps_enabled_usage'].includes(key)) return 'tone-success'
  if (['groups_removed', 'scheduling_disabled', 'already_quarantined', 'bps_kept_usage', 'bps_already_enabled', 'failure_counted', 'restore_counted'].includes(key)) return 'tone-muted'
  return 'tone-neutral'
}
function actionExplanation(action: string) {
  const key = actionKey(action), bpsKey = `qualityOps.bpsActionHelp.${key}`
  if (detailBPS.value && te(bpsKey)) return t(bpsKey)
  return te(`qualityOps.actionHelp.${key}`) ? t(`qualityOps.actionHelp.${key}`) : t('qualityOps.actionHelp.no_change')
}
const probeLabels: Record<StateProbeVerdict, string> = { healthy: 'qualityOps.probeHealthy', degraded: 'qualityOps.probeDegraded', inconclusive: 'qualityOps.probeInconclusive' }
function isProbePlan(plan: ScheduledTestPlan) { return plan.pelican_config?.question_kind === STATE_PROBE_QUESTION }
function isProbeResult(result: ScheduledTestResult) { return result.pelican_config?.question_kind === STATE_PROBE_QUESTION }
function resultLabel(result: ScheduledTestResult) {
  if (!result.status) return t('qualityOps.notLoaded')
  const verdict = stateProbeVerdict(result)
  if (verdict) return t(probeLabels[verdict])
  return t(result.status === 'success' ? 'qualityOps.passed' : result.error_message === 'answer_mismatch' ? 'qualityOps.wrongAnswer' : result.quality_judgment?.verdict === 'unknown' || result.error_message?.startsWith('judge_') ? 'qualityOps.judgeUnknown' : 'qualityOps.requestError')
}
// 探针「无法判断」不是账号问题，用中性色，避免和降智混在一起。
function resultTextClass(result: ScheduledTestResult) {
  if (!result.status) return 'text-gray-400'
  if (stateProbeVerdict(result) === 'inconclusive') return 'text-gray-500'
  return result.status === 'success' ? 'text-emerald-600' : 'text-rose-600'
}
function resultTone(result: ScheduledTestResult) {
  if (stateProbeVerdict(result) === 'inconclusive') return 'tone-neutral'
  return result.status === 'success' ? 'tone-success' : 'tone-danger'
}
function defaults() {
  return { model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *', enabled: true, max_results: 100, auto_recover: false,
    pelican_config: { question_kind: 'candy' as 'candy' | typeof STATE_PROBE_QUESTION, prompt: CANDY_PROMPT, reasoning_effort: 'high', parallel_count: 1,
      quality: { expected_answer: '21', action: 'remove_groups' as QualityPolicy['action'], remove_group_ids: [] as number[], auto_restore: false, judge: { group_id: 0, model_id: '', prompt: t('qualityOps.defaultJudgePrompt') }, bps: defaultQualityBPS() } } }
}
const form = ref(defaults())
const isProbe = computed(() => form.value.pelican_config.question_kind === STATE_PROBE_QUESTION)
const bps = computed(() => form.value.pelican_config.quality.bps)
// BPS 设置可见时，「满血后自动关闭 BPS」放在设置里，底部的通用恢复开关不再重复出现。
const bpsSettingsShown = computed(() => editsField('action') && isProbe.value && form.value.pelican_config.quality.action === 'enable_bps')
// 与账号批量编辑的 403 目标分组候选一致：OpenAI 分组，非简易模式下另含混合分组。
const bpsTargetGroups = computed(() => groups.value.filter(group => group.platform === 'openai' || (!auth.isSimpleMode && group.platform === 'composite')))
const formSnapshot = () => JSON.stringify([form.value, selectedAccounts.value, bulkFields.value])
function message(e: unknown) { const err = e as { response?: { data?: { message?: string; error?: string } }; message?: string }; return err.response?.data?.message || err.response?.data?.error || err.message || t('qualityOps.error') }
async function load() { await store.refresh() }
async function refreshOperations() { await store.refreshOperations() }
async function moreOperations() { await store.refreshOperations(true) }
async function searchAccounts(page = 1) {
  accountSelectionRequest++; selectingAccounts.value = false
  const request = ++accountRequest
  accountsLoading.value = true; accountsError.value = ''; accounts.value = []
  try {
    const data = await accountsAPI.list(page, 50, accountFilters())
    if (!alive || request !== accountRequest) return
    accounts.value = data.items; accountPage.value = page; accountPages.value = Math.max(1, Math.ceil(data.total / 50))
  } catch (e) { if (alive && request === accountRequest) accountsError.value = message(e) }
  finally { if (request === accountRequest) accountsLoading.value = false }
}
function selectCurrentPage() {
  if (accountsLoading.value || selectingAccounts.value) return
  selectedAccounts.value = [...new Set([...selectedAccounts.value, ...selectableAccounts.value.map(account => account.id)])]
}
function clearAccountSelection() {
  accountSelectionRequest++; selectingAccounts.value = false; selectedAccounts.value = []
}
async function selectMatchingAccounts() {
  if (accountsLoading.value || selectingAccounts.value || busy.value) return
  const request = ++accountSelectionRequest, filters = accountFilters()
  selectingAccounts.value = true; accountsError.value = ''
  const ids = new Set<number>()
  try {
    // Collect every matching page before changing selection so a failed page never leaves a partial batch.
    for (let page = 1, pages = 1; page <= pages; page++) {
      const data = await accountsAPI.list(page, 50, filters)
      if (!alive || request !== accountSelectionRequest) return
      pages = Math.max(1, Math.ceil(data.total / 50))
      for (const account of data.items) ids.add(account.id)
    }
    selectedAccounts.value = [...new Set([...selectedAccounts.value, ...ids])].filter(id => !existingAccountIds.value.has(id))
  } catch (e) { if (alive && request === accountSelectionRequest) accountsError.value = message(e) }
  finally { if (request === accountSelectionRequest) selectingAccounts.value = false }
}
function newPlan() {
  bulkEditing.value = false; bulkRuleIds.value = []; bulkFields.value = []; bulkProgress.value = ''
  closeDetails(); invalidateAccountRequests(); error.value = ''; editing.value = null; form.value = defaults()
  selectedAccounts.value = []; search.value = ''; accountGroup.value = ''; accountType.value = ''
  showForm.value = true; initialForm.value = formSnapshot(); void searchAccounts()
  if (!groups.value.length) void store.refreshGroups()
}
function edit(plan: ScheduledTestPlan) {
  bulkEditing.value = false; bulkRuleIds.value = []; bulkFields.value = []; bulkProgress.value = ''
  closeDetails(); error.value = ''; editing.value = plan.id; selectedAccounts.value = []
  form.value = { ...defaults(), model_id: plan.model_id, cron_expression: plan.cron_expression, enabled: plan.enabled, max_results: plan.max_results, pelican_config: { ...defaults().pelican_config, ...JSON.parse(JSON.stringify(plan.pelican_config || {})) } }
  form.value.pelican_config.quality.judge ||= defaults().pelican_config.quality.judge
  form.value.pelican_config.quality.bps = qualityBPSForm(form.value.pelican_config.quality.bps)
  showForm.value = true; initialForm.value = formSnapshot(); void loadJudgeModels()
}
function editSelectedRules() {
  if (busy.value || selectedRulesPending.value) return
  const selected = plans.value.filter(plan => selectedRuleIds.value.includes(plan.id))
  if (!selected.length) return
  // Start from the first selected rule for convenience, but no field is applied
  // until explicitly checked. The request builder preserves each other value.
  edit(selected[0])
  editing.value = null; bulkEditing.value = true
  bulkRuleIds.value = selected.map(plan => plan.id)
  initialForm.value = formSnapshot()
}
function closeForm() {
  if (busy.value) return
  if (initialForm.value !== formSnapshot()) { discardPrompt.value = true; return }
  showForm.value = false
}
// 新建规则选「开启 BPS」时默认勾上满血后自动关闭，改回其它处理方式时回到默认不勾（移出分组等仍需手动开启恢复）；
// 编辑已有规则或批量修改时保留原值。sync：打开表单时的替换在记录 initialForm 前就处理完。
watch(() => form.value.pelican_config.quality.action, (next, prev) => {
  if (editing.value || bulkEditing.value || (next !== 'enable_bps' && prev !== 'enable_bps')) return
  form.value.pelican_config.quality.auto_restore = next === 'enable_bps'
}, { flush: 'sync' })
function useCandy() { form.value.pelican_config.prompt = CANDY_PROMPT; form.value.pelican_config.quality.expected_answer = '21' }
function selectQuestionKind() {
  const config = form.value.pelican_config
  if (config.question_kind === STATE_PROBE_QUESTION) { config.parallel_count = 1; return }
  // 开 BPS 只认探针结论：切回糖果题时退回默认处理方式。
  if (config.quality.action === 'enable_bps') config.quality.action = 'remove_groups'
  config.quality.judge ||= defaults().pelican_config.quality.judge
  if (!config.prompt.trim()) useCandy()
}
// 探针规则不发题目、不走判题模型：提交前去掉题目、参考答案和判题配置，并行固定为 1。
// BPS 设置只随「开启 BPS」提交。
function payload() {
  const quality = form.value.pelican_config.quality
  if (!isProbe.value) return { ...form.value, pelican_config: { ...form.value.pelican_config, quality: { ...quality, bps: undefined } } }
  const { action, remove_group_ids, auto_restore } = quality
  return { ...form.value, pelican_config: { ...form.value.pelican_config, prompt: '', parallel_count: 1,
    quality: { expected_answer: '', action, remove_group_ids: action === 'remove_groups' ? [...remove_group_ids] : [], auto_restore,
      bps: action === 'enable_bps' ? qualityBPSPayload(quality.bps) : undefined } } }
}
async function save() {
  if (busy.value || selectingAccounts.value) return
  if (bulkEditing.value) { await saveBulkRules(); return }
  busy.value = true; error.value = ''; notice.value = ''
  const scope = identity()
  let changed = false
  try {
    const judge = form.value.pelican_config.quality.judge
    if (!isProbe.value && (!judge.group_id || !judge.model_id.trim() || !judge.prompt.trim())) throw new Error(t('qualityOps.configureJudge'))
    if (form.value.pelican_config.quality.action === 'remove_groups' && !form.value.pelican_config.quality.remove_group_ids.length) throw new Error(t('qualityOps.selectGroups'))
    if (form.value.pelican_config.quality.action === 'enable_bps') {
      if (!isProbe.value) throw new Error(t('qualityOps.bpsRequiresProbe'))
      const invalid = qualityBPSError(bps.value)
      if (invalid) throw new Error(t(invalid))
    }
    const body = payload()
    if (editing.value) { await scheduledTests.update(editing.value, body); changed = true }
    else {
      for (const id of [...selectedAccounts.value]) {
        await scheduledTests.create({ ...body, account_id: id }); changed = true
        if (!alive || scope !== identity()) return
        selectedAccounts.value = selectedAccounts.value.filter(value => value !== id)
      }
    }
    if (!alive || scope !== identity()) return
    showForm.value = false; notice.value = t('qualityOps.saved'); await store.refreshRules(true)
  } catch (e) { if (alive && scope === identity()) { error.value = message(e); if (changed) await store.refreshRules(true) } }
  finally { busy.value = false }
}
async function saveBulkRules() {
  if (!bulkRuleIds.value.length || !bulkFields.value.length) return
  busy.value = true; error.value = ''; notice.value = ''; bulkProgress.value = ''
  const scope = identity(), ids = [...bulkRuleIds.value]
  const failed: number[] = [], failures: string[] = []
  let completed = 0
  try {
    // Refresh before merging nested config, then validate the whole batch before
    // issuing any writes. A deleted rule must not silently disappear from a save.
    await store.refreshRules(true)
    if (!alive || scope !== identity()) return
    if (store.rulesError) throw new Error(store.rulesError)
    const requests = ids.map(id => {
      const plan = plans.value.find(item => item.id === id)
      if (!plan) throw new Error(t('qualityOps.ruleUnavailable', { id }))
      return { id, body: buildQualityRulePatch(plan, form.value, bulkFields.value) }
    })
    for (const { id, body } of requests) {
      if (!alive || scope !== identity()) return
      try {
        await scheduledTests.update(id, body)
        if (!alive || scope !== identity()) return
        selectedRuleIds.value = selectedRuleIds.value.filter(value => value !== id)
        bulkRuleIds.value = bulkRuleIds.value.filter(value => value !== id)
      } catch (e) {
        if (!alive || scope !== identity()) return
        failed.push(id); failures.push(`#${id}: ${message(e)}`)
      }
      completed++
      bulkProgress.value = t('qualityOps.bulkProgress', { completed, total: ids.length })
    }
    if (failed.length) {
      bulkRuleIds.value = failed
      error.value = `${t('qualityOps.bulkPartial', { saved: ids.length - failed.length, failed: failed.length })} ${failures.join(' / ')}`
    } else {
      showForm.value = false
      notice.value = t('qualityOps.bulkSaved', { count: ids.length })
    }
    await store.refreshRules(true)
  } catch (e) {
    if (alive && scope === identity()) {
      const detail = message(e)
      error.value = te(detail) ? t(detail) : detail
    }
  } finally { busy.value = false }
}
async function planAction(plan: ScheduledTestPlan, kind: string, fn: () => Promise<unknown>) {
  if (pending.value[plan.id]) return
  pending.value[plan.id] = kind; error.value = ''; notice.value = ''
  const scope = identity()
  try {
    await fn()
    if (!alive || scope !== identity()) return
    if (kind === 'run') notice.value = t('qualityOps.queued')
    await store.refreshRules(true)
  } catch (e) { if (alive && scope === identity()) error.value = message(e) }
  finally { delete pending.value[plan.id] }
}
async function toggle(plan: ScheduledTestPlan) { await planAction(plan, 'toggle', () => scheduledTests.update(plan.id, { enabled: !plan.enabled })) }
async function run(plan: ScheduledTestPlan) { await planAction(plan, 'run', () => runQualityPlan(plan.id)) }
async function confirmDelete() {
  if (!deleteTarget.value || deleting.value) return
  deleting.value = true
  const id = deleteTarget.value.id, scope = identity()
  try {
    await scheduledTests.delete(id)
    if (!alive || scope !== identity()) return
    deleteTarget.value = null; showForm.value = false; if (historyPlan.value?.id === id) closeDetails()
    await Promise.all([store.refreshRules(true), store.refreshOperations()])
  } catch (e) { error.value = message(e) }
  finally { deleting.value = false }
}
async function loadJudgeModels() {
  const request = ++judgeModelsRequest
  const id = form.value.pelican_config.quality.judge.group_id
  judgeModels.value = []
  if (!id) return
  try { const models = await groupsAPI.getModelAllowlistCandidates(id); if (alive && request === judgeModelsRequest) judgeModels.value = models }
  catch { if (alive && request === judgeModelsRequest) error.value = t('qualityOps.judgeModelsUnavailable') }
}
function closeDetails() { detailRequest++; answerRequest++; historyPlan.value = null; detailOperation.value = null; selectedResult.value = null; selectedResultId.value = null; results.value = []; detailsError.value = ''; detailsLoading.value = answerLoading.value = false; loadedAnswers.clear() }
async function history(plan: ScheduledTestPlan) {
  closeDetails(); historyPlan.value = plan; detailsLoading.value = true
  const request = ++detailRequest
  try {
    const data = await scheduledTests.listResults(plan.id, 100, false)
    if (!alive || request !== detailRequest) return
    results.value = data
    if (data[0]) await selectResult(data[0])
  } catch (e) { if (alive && request === detailRequest) detailsError.value = message(e) }
  finally { if (request === detailRequest) detailsLoading.value = false }
}
async function operationDetails(operation: QualityOperation) {
  closeDetails(); detailOperation.value = operation
  historyPlan.value = plans.value.find(p => p.id === operation.plan_id) || { id: operation.plan_id, account_id: operation.account_id, account_name: operation.account_name } as ScheduledTestPlan
  results.value = operation.result_ids.map(id => ({ ...operation, id, status: '', error_message: '', response_text: '' }))
  if (results.value[0]) await selectResult(results.value[0])
}
async function selectResult(result: ScheduledTestResult) {
  if (!historyPlan.value) return
  const request = ++answerRequest, planId = historyPlan.value.id
  selectedResultId.value = result.id; selectedResult.value = null; detailsError.value = ''
  const cached = loadedAnswers.get(result.id)
  if (cached) { selectedResult.value = cached; answerLoading.value = false; return }
  answerLoading.value = true
  try {
    const answer = await scheduledTests.getResult(planId, result.id)
    if (!alive || request !== answerRequest) return
    loadedAnswers.set(result.id, answer); selectedResult.value = answer
    results.value = results.value.map(item => item.id === result.id ? answer : item)
  } catch (e) { if (alive && request === answerRequest) detailsError.value = message(e) }
  finally { if (request === answerRequest) answerLoading.value = false }
}
function retryDetails() {
  if (selectedResultId.value !== null) { const result = results.value.find(r => r.id === selectedResultId.value); if (result) void selectResult(result) }
  else if (historyPlan.value) void history(historyPlan.value)
}
function navigateOperation(offset: number) { const next = filteredOperations.value[operationIndex.value + offset]; if (next) void operationDetails(next) }
watch(() => identity(), () => { error.value = notice.value = ''; closeDetails(); showForm.value = false; discardPrompt.value = false; deleteTarget.value = null; accounts.value = []; selectedRuleIds.value = []; bulkRuleIds.value = []; bulkEditing.value = false; accountRequest++; judgeModelsRequest++ })
onMounted(() => {
  void load()
  poll = setInterval(() => { if (document.visibilityState === 'visible' && !refreshing.value) { void store.refreshRules(); void store.refreshOperations() } }, 30_000)
})
onBeforeUnmount(() => { alive = false; accountSelectionRequest++; detailRequest++; answerRequest++; accountRequest++; judgeModelsRequest++; if (poll) clearInterval(poll); loadedAnswers.clear() })
</script>

<style scoped>
.quality-workspace { height: calc(100dvh - 8rem - 1px); min-height: 34rem; @apply flex w-full min-w-0 flex-col gap-4 text-gray-900 dark:text-gray-100; }
.quality-workspace > :not(.workspace-columns) { flex-shrink: 0; }
.quality-workspace > nav { margin-bottom: 0; max-width: 100%; }
.workspace-heading { @apply flex flex-wrap items-center justify-between gap-4; }
.workspace-eyebrow { @apply mb-1 text-[11px] font-semibold tracking-widest text-primary-600 dark:text-primary-400; }
.workspace-heading h2 { @apply text-2xl font-semibold tracking-tight; }
.workspace-subtitle { @apply mt-1.5 text-sm text-gray-500 dark:text-gray-400; }
.heading-actions { @apply flex flex-wrap items-center gap-2; }
.heading-actions .btn, .detail-footer .btn { @apply inline-flex items-center gap-2; }
.updated-label { @apply mr-2 hidden text-xs tabular-nums text-gray-400 xl:block; }
.workspace-message { @apply flex items-center justify-between gap-4 rounded-xl border px-4 py-3 text-sm; }
.message-error, .editor-error { @apply border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300; }
.message-success { @apply border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300; }
.workspace-summary { @apply grid grid-cols-2 rounded-2xl border border-gray-200/80 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900 lg:grid-cols-4; }
.summary-cell { @apply relative flex items-center gap-3 px-5 py-4; }
.summary-cell + .summary-cell { @apply lg:border-l lg:border-gray-100 lg:dark:border-dark-700; }
.summary-icon { @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-slate-100 text-slate-500 dark:bg-dark-800 dark:text-gray-400; }
.summary-icon.is-teal { @apply bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40; }
.summary-icon.is-amber { @apply bg-amber-50 text-amber-600 dark:bg-amber-950/40; }
.summary-cell div > span { @apply block text-xs text-gray-500 dark:text-gray-400; }
.summary-cell strong { @apply mt-1 block text-2xl font-semibold tabular-nums tracking-tight; }
.summary-cell strong small { @apply text-sm font-normal text-gray-400; }
.summary-link { @apply ml-auto inline-flex items-center gap-1 text-xs text-primary-600; }
.workspace-columns { display: grid; grid-template-columns: clamp(320px, 30%, 480px) minmax(0, 1fr); gap: 16px; flex: 1; min-height: 0; }
.workspace-panel { @apply flex min-h-0 min-w-0 flex-col overflow-hidden rounded-2xl border border-gray-200/80 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900; }
.panel-heading { @apply flex shrink-0 items-center justify-between gap-3 px-5 pb-4 pt-5; }
.panel-heading h3 { @apply flex items-center gap-2 text-base font-semibold; }
.panel-heading p { @apply mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.count-label { @apply rounded-md bg-gray-100 px-1.5 py-0.5 text-xs tabular-nums text-gray-500 dark:bg-dark-800; }
.icon-button { @apply flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-gray-200 text-gray-500 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800; }
.rule-search { @apply mx-4 mb-3 flex shrink-0 items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 text-gray-400 dark:border-dark-700 dark:bg-dark-800; }
.rule-search input { @apply h-9 min-w-0 flex-1 bg-transparent text-sm text-gray-800 outline-none dark:text-gray-100; }
.all-accounts { @apply mx-4 mb-3 flex shrink-0 items-center gap-2 rounded-lg px-3 py-2.5 text-left text-sm text-gray-500 dark:text-gray-400; }
.rule-selection-toolbar { @apply mx-4 mb-3 flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 text-xs text-gray-500 dark:text-gray-400; }
.rule-check-all { @apply flex cursor-pointer items-center gap-2; }
.rule-selection-toolbar > button:not(.bulk-edit-button) { @apply text-primary-600; }
.bulk-edit-button { @apply ml-auto inline-flex items-center gap-1.5 px-3 py-2 text-xs; }
.rule-checkbox { @apply flex h-8 shrink-0 cursor-pointer items-center; }
.rule-checkbox input, .rule-check-all input, .bulk-fields input { @apply h-4 w-4 shrink-0 cursor-pointer accent-primary-600; }
.bulk-editor-intro { @apply space-y-3 rounded-xl border border-primary-100 bg-primary-50/50 p-4 text-sm dark:border-primary-900 dark:bg-primary-950/20; }
.bulk-editor-intro p { @apply leading-relaxed text-gray-600 dark:text-gray-300; }
.bulk-editor-intro summary { @apply cursor-pointer text-primary-700 dark:text-primary-300; }
.bulk-editor-intro ul { @apply mt-2 max-h-32 space-y-1 overflow-auto break-words text-xs text-gray-500; }
.bulk-fields { @apply grid gap-3 sm:grid-cols-2; }
.bulk-fields label { @apply flex cursor-pointer items-center gap-2; }
.all-accounts span:last-child { @apply ml-auto text-xs tabular-nums; }
.all-accounts.selected { @apply bg-primary-50 font-medium text-primary-700 dark:bg-primary-950/40 dark:text-primary-300; }
.rules-scroll { @apply min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain px-4 pb-4; scrollbar-gutter: stable; }
.rule-card { @apply rounded-xl border border-gray-200 p-3.5 transition-colors dark:border-dark-700; }
.rule-card.selected { @apply border-primary-300 bg-primary-50/30 ring-1 ring-primary-100 dark:border-primary-700 dark:bg-primary-950/20 dark:ring-primary-900; }
.rule-card.checked { @apply border-primary-400 bg-primary-50/60 dark:border-primary-600 dark:bg-primary-950/40; }
.rule-card-top { @apply flex items-start gap-2; }
.rule-select { @apply flex min-w-0 flex-1 items-start gap-2.5 text-left; }
.account-avatar { @apply flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-slate-100 text-xs font-semibold text-slate-600 dark:bg-dark-700 dark:text-gray-300; }
.rule-select strong { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; @apply text-[13px] font-semibold leading-5; }
.rule-meta { @apply mt-1 flex gap-1.5 text-[11px] tabular-nums text-gray-400; }
.state-toggle { @apply inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 py-1 text-[10px] font-medium; }
.state-toggle > span, .outcome-badge > span { @apply h-1.5 w-1.5 rounded-full bg-current; }
.state-enabled { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400; }
.state-paused { @apply bg-gray-100 text-gray-500 dark:bg-dark-800; }
.rule-model { @apply mt-3 flex flex-wrap items-center gap-2 text-[11px] text-gray-600 dark:text-gray-400; }
.rule-model code { overflow-wrap: anywhere; @apply min-w-0 rounded bg-gray-100 px-1.5 py-0.5 dark:bg-dark-800; }
.running-label { @apply text-primary-600; }
.probe-tag { @apply rounded bg-violet-50 px-1.5 py-0.5 text-violet-700 dark:bg-violet-950/40 dark:text-violet-300; }
.rule-target { @apply mt-2 flex min-w-0 items-center gap-1.5 text-[11px] text-gray-500; }
.rule-target span { @apply truncate; }
.rule-schedule { @apply mt-3 flex justify-between gap-2 text-[11px] tabular-nums text-gray-400; }
.rule-schedule time { @apply text-gray-600 dark:text-gray-300; }
.rule-warning { @apply mt-2 text-xs text-amber-600; }
.rule-actions { @apply mt-3 flex items-center gap-1 border-t border-gray-100 pt-2 dark:border-dark-700; }
.rule-actions button { @apply inline-flex min-h-8 flex-1 items-center justify-center gap-1 rounded-md text-[11px] text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-800 dark:hover:text-gray-100; }
.rule-actions button:first-child { @apply text-primary-600; }
.operations-toolbar { @apply flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-t border-gray-100 bg-gray-50/60 px-5 py-3 dark:border-dark-700 dark:bg-dark-800/50; }
.scope-label { overflow-wrap: anywhere; @apply flex min-w-0 items-center gap-2 text-xs font-medium text-gray-600 dark:text-gray-300; max-width: 65%; }
.scope-dot { @apply h-1.5 w-1.5 shrink-0 rounded-full bg-primary-500; }
.operations-toolbar select { @apply max-w-full rounded-lg border border-gray-200 bg-white px-2 py-1.5 text-xs text-gray-600 dark:border-dark-600 dark:bg-dark-900 dark:text-gray-300; }
.operations-scroll { @apply min-h-0 flex-1 overflow-auto overscroll-contain; scrollbar-gutter: stable; }
.operations-table { @apply w-full text-left text-xs; min-width: 620px; }
.operations-table thead { @apply sticky top-0 z-10 bg-white text-gray-400 dark:bg-dark-900; box-shadow: 0 1px 0 rgb(148 163 184 / .15); }
.operations-table th { @apply whitespace-nowrap px-4 py-3 font-medium; }
.operations-table td { @apply border-b border-gray-100 px-4 py-4 align-middle dark:border-dark-800; }
.operations-table tbody tr:hover, .selected-row { @apply bg-slate-50/80 dark:bg-dark-800/70; }
.time-cell { @apply whitespace-nowrap tabular-nums; }
.time-cell strong { @apply block text-xs font-medium; }
.time-cell > span, .account-cell > span { @apply mt-1 block text-[10px] text-gray-400; }
.account-cell { max-width: 220px; }
.account-cell button { @apply max-w-full text-left hover:text-primary-600; }
.account-cell strong { @apply block truncate font-medium; max-width: 190px; }
.test-count { @apply inline-flex items-center gap-1.5 whitespace-nowrap font-medium tabular-nums; }
.test-passed { @apply text-emerald-600 dark:text-emerald-400; }
.test-other { @apply text-rose-500 dark:text-rose-400; }
.cell-secondary { @apply mt-1.5 block truncate text-[10px] text-gray-400; max-width: 170px; }
.outcome-badge { @apply inline-flex items-center gap-1.5 whitespace-nowrap rounded-md px-2 py-1 text-[11px] font-medium; }
.tone-success { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300; }
.tone-warning { @apply bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300; }
.tone-muted { @apply bg-slate-100 text-slate-600 dark:bg-dark-700 dark:text-gray-300; }
.tone-neutral { @apply bg-gray-100 text-gray-500 dark:bg-dark-800; }
.tone-danger { @apply bg-rose-50 text-rose-600 dark:bg-rose-950/40 dark:text-rose-300; }
.detail-button { @apply inline-flex min-h-8 items-center gap-1 whitespace-nowrap rounded-md px-1 text-xs text-gray-400 hover:text-primary-600; }
.operations-footer { @apply flex min-h-14 shrink-0 items-center justify-between gap-3 border-t border-gray-100 px-5 text-[11px] text-gray-400 dark:border-dark-700; }
.operations-footer button { @apply inline-flex items-center gap-1.5 text-primary-600; }
.panel-error { @apply m-3 flex items-center justify-between gap-3 rounded-lg bg-red-50 p-3 text-xs text-red-700 dark:bg-red-950/40 dark:text-red-300; }
.panel-error button { @apply shrink-0 underline; }
.panel-empty { @apply flex min-h-52 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-gray-400; }
.panel-empty p { @apply max-w-sm text-xs leading-relaxed; }
.rule-skeleton { @apply space-y-3 rounded-xl border border-gray-100 p-5 dark:border-dark-700; }
.cell-skeleton, .rule-skeleton span { @apply block h-3 rounded bg-gray-100 dark:bg-dark-700; animation: quality-pulse 1.7s ease-in-out infinite; }
.rule-skeleton span:first-child { @apply h-5 w-3/4; }
.rule-skeleton span:last-child { @apply w-1/2; }
.skeleton-stack { @apply space-y-3; }
.detail-heading { @apply mb-5 flex items-center gap-3; }
.detail-heading h3 { @apply text-base font-semibold; }
.detail-heading p { @apply mt-1 text-xs text-gray-400; }
.account-management-link { @apply ml-auto inline-flex shrink-0 items-center gap-1 text-xs text-primary-600; }
.round-overview { @apply mb-5 grid grid-cols-2 divide-x divide-gray-200 rounded-xl border border-gray-200 bg-gray-50 dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-800; }
.round-overview > div { @apply p-4; }
.round-overview span { @apply block text-xs text-gray-500; }
.round-overview strong { @apply mt-2 block text-sm font-semibold; }
.conflict-explanation { @apply mb-5 flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50/80 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200; }
.conflict-explanation > svg { @apply mt-0.5 shrink-0; }
.conflict-explanation h4 { @apply mb-2 text-sm font-semibold; }
.conflict-explanation p, .conflict-explanation li, .conflict-explanation strong { @apply text-xs leading-6; }
.conflict-causes summary { @apply cursor-pointer text-xs font-medium leading-7; }
.conflict-explanation ul { @apply my-2 list-disc pl-4; }
.conflict-limit { @apply mb-2 text-amber-700 dark:text-amber-400; }
.action-explanation { @apply mb-4 flex gap-2 rounded-lg bg-gray-50 p-3 text-xs leading-relaxed text-gray-500 dark:bg-dark-800 dark:text-gray-400; }
.action-explanation svg { @apply shrink-0; }
.detail-policy { @apply mb-5 space-y-2 text-xs; }
.detail-policy > div { @apply flex justify-between gap-4; }
.detail-policy dt { @apply shrink-0 text-gray-400; }
.detail-policy dd { @apply text-right text-gray-600 dark:text-gray-300; }
.detail-grid { display: grid; grid-template-columns: 145px minmax(0, 1fr); @apply gap-4; }
.result-navigation { @apply max-h-[55dvh] space-y-2 overflow-y-auto; }
.result-navigation > p { @apply mb-3 flex justify-between text-xs text-gray-400; }
.result-navigation button { @apply block w-full rounded-lg border border-transparent p-2.5 text-left text-xs hover:bg-gray-50 dark:hover:bg-dark-800; }
.result-navigation button.selected { @apply border-primary-200 bg-primary-50 dark:border-primary-800 dark:bg-primary-950/30; }
.result-navigation button span { @apply block; }
.result-navigation button span + span { @apply mt-1.5 text-[10px]; }
.answer-detail { @apply min-w-0 rounded-xl border border-gray-200 p-4 dark:border-dark-700; }
.answer-heading { @apply mb-4 flex items-center justify-between gap-2; }
.answer-heading h4 { @apply text-sm font-semibold; }
.answer-reference { @apply rounded-lg bg-emerald-50/60 p-3 dark:bg-emerald-950/20; }
.answer-reference span { @apply text-[11px] text-emerald-700 dark:text-emerald-400; }
.answer-reference p { @apply mt-1 whitespace-pre-wrap break-words text-sm; }
.response-heading { @apply mb-2 mt-4 flex justify-between text-xs text-gray-500; }
.response-content { @apply max-h-[40dvh] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-4 font-sans text-sm leading-7 text-gray-800 dark:bg-dark-800 dark:text-gray-200; }
.judge-reason { @apply mt-4 border-t border-gray-100 pt-4 dark:border-dark-700; }
.judge-reason h5 { @apply text-xs font-semibold; }
.judge-reason p { @apply mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-gray-600 dark:text-gray-300; }
.judge-reason > span { @apply mt-3 block text-[10px] text-gray-400; }
.detail-loading { @apply space-y-4 py-8 text-xs text-gray-400; }
.detail-footer { @apply flex items-center justify-between gap-2 text-xs text-gray-400; }
.editor-footer { @apply flex items-center gap-2; }
.editor-error { @apply mb-4 rounded-lg border p-3 text-sm; }
.delete-rule { @apply text-sm text-red-600; }
.quality-editor label, .quality-editor legend { @apply text-sm; }
.quality-editor .input { @apply w-full; }
button:disabled { @apply cursor-not-allowed opacity-40; }
button:focus-visible, input:focus-visible, select:focus-visible, a:focus-visible { @apply outline-none ring-2 ring-primary-400 ring-offset-2 dark:ring-offset-dark-900; }
@keyframes quality-pulse { 50% { opacity: .45; } }
@media (min-width: 1280px) and (max-height: 900px) {
  .quality-workspace { gap: 12px; }
  .workspace-eyebrow, .workspace-subtitle { display: none; }
  .workspace-heading h2 { font-size: 20px; }
  .summary-cell { padding-top: 10px; padding-bottom: 10px; }
  .summary-cell strong { font-size: 20px; }
  .panel-heading { padding-top: 12px; padding-bottom: 12px; }
  .panel-heading p { display: none; }
  .rule-card { padding: 12px; }
  .rule-model, .rule-schedule, .rule-actions { margin-top: 8px; }
}
@media (max-width: 1279px) {
  .quality-workspace { height: auto; min-height: 0; }
  .workspace-columns { grid-template-columns: minmax(0, 1fr); }
  .rules-panel { height: clamp(24rem, 60dvh, 42rem); }
  .operations-panel { height: clamp(28rem, 70dvh, 52rem); }
}
@media (max-width: 640px) {
  .operations-table { display: block; min-width: 0; }
  .operations-table thead { @apply sr-only; }
  .operations-table tbody { display: block; }
  .operations-table tbody tr { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 12px; padding: 16px; @apply border-b border-gray-100 dark:border-dark-800; }
  .operations-table td { min-width: 0; max-width: none; padding: 0; border: 0; }
  .operations-table td::before { content: attr(data-label); @apply mb-1.5 block text-[10px] text-gray-400; }
  .operations-table .account-cell { grid-column: 1 / -1; grid-row: 1; }
  .operations-table .account-cell strong { max-width: none; white-space: normal; overflow-wrap: anywhere; }
  .operations-table .action-cell { grid-column: 1 / -1; }
  .operations-table .detail-cell { grid-column: 1 / -1; text-align: right; }
  .operations-table .outcome-badge { white-space: normal; text-align: left; }
  .operations-table .cell-secondary { max-width: 100%; }
  .operations-footer { @apply flex-wrap gap-1 px-4 py-3; }
  .scope-label { max-width: 100%; }
  .editor-footer { flex-wrap: wrap; }
}
@media (max-width: 640px) { .summary-cell { padding: 14px 12px; gap: 8px; } .summary-cell strong { font-size: 22px; } .summary-icon { width: 32px; height: 32px; } .summary-link { display: none; } .heading-actions { width: 100%; justify-content: flex-end; } .detail-grid { grid-template-columns: 1fr; } .result-navigation { display: flex; gap: 8px; overflow-x: auto; } .result-navigation > p { display: none; } .result-navigation button { min-width: 100px; } .account-management-link { font-size: 10px; } .detail-footer .btn { font-size: 11px; padding: 8px; } }
@media (prefers-reduced-motion: reduce) { .cell-skeleton, .rule-skeleton span { animation: none; } }
</style>
