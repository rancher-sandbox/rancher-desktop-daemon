<script lang="ts">
import { defineComponent } from 'vue';
import { mapGetters } from 'vuex';

export default defineComponent({
  name:     'dashboard-open',
  computed: {
    ...mapGetters('preferences', ['getPreferences']),
    kubernetesEnabled(): boolean {
      return this.getPreferences.kubernetes.enabled;
    },
    kubernetesStarted(): boolean {
      return false;
    },
  },
  methods: {
    openDashboard() {
      this.$emit('open-dashboard');
    },
  },
});
</script>

<template>
  <button
    v-if="kubernetesEnabled"
    :disabled="!kubernetesStarted"
    class="btn role-secondary btn-icon-text"
    @click="openDashboard"
  >
    {{ t('nav.userMenu.clusterDashboard') }}
  </button>
</template>
