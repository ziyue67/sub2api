/**
 * Onboarding Store
 * Manages onboarding tour state and control methods
 */
import { defineStore } from 'pinia';
import { markRaw, ref, shallowRef } from 'vue';
export const useOnboardingStore = defineStore('onboarding', () => {
    const replayCallback = ref(null);
    const nextStepCallback = ref(null);
    const isCurrentStepCallback = ref(null);
    // 全局 driver 实例，跨组件保持
    const driverInstance = shallowRef(null);
    function setReplayCallback(callback) {
        replayCallback.value = callback;
    }
    function setControlMethods(methods) {
        nextStepCallback.value = methods.nextStep;
        isCurrentStepCallback.value = methods.isCurrentStep;
    }
    function clearControlMethods() {
        nextStepCallback.value = null;
        isCurrentStepCallback.value = null;
    }
    function setDriverInstance(driver) {
        driverInstance.value = driver ? markRaw(driver) : null;
    }
    function getDriverInstance() {
        return driverInstance.value;
    }
    function isDriverActive() {
        return driverInstance.value?.isActive?.() ?? false;
    }
    function replay() {
        if (replayCallback.value) {
            replayCallback.value();
        }
    }
    /**
     * Manually advance to the next step
     * @param delay Optional delay in ms (useful for waiting for animations)
     */
    async function nextStep(delay = 0) {
        if (nextStepCallback.value) {
            await nextStepCallback.value(delay);
        }
    }
    /**
     * Check if the tour is currently highlighting a specific element
     */
    function isCurrentStep(selector) {
        if (isCurrentStepCallback.value) {
            return isCurrentStepCallback.value(selector);
        }
        return false;
    }
    return {
        setReplayCallback,
        setControlMethods,
        clearControlMethods,
        setDriverInstance,
        getDriverInstance,
        isDriverActive,
        replay,
        nextStep,
        isCurrentStep
    };
});
