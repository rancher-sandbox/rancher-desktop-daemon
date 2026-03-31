<template>
  <badge-state
    v-if="currentContainer"
    :color="isRunning ? 'bg-success' : 'bg-darker'"
    :label="containerState"
    data-testid="container-state"
  />
</template>

<script>
import { BadgeState } from '@rancher/components';
import { defineComponent } from 'vue';

import { mapTypedGetters } from '@pkg/entry/store';

export default defineComponent({
  name:       'ContainerStatusBadge',
  components: { BadgeState },
  computed:   {
    ...mapTypedGetters('container-engine', ['containerById']),
    containerId() {
      return this.$route.params.id || '';
    },
    currentContainer() {
      return this.containerById(this.containerId) ?? null;
    },
    containerState() {
      return this.currentContainer?.status?.status || 'unknown';
    },
    isRunning() {
      return this.containerState === 'running';
    },
  },
});
</script>
