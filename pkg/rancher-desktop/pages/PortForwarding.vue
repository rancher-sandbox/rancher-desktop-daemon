<template>
  <PortForwarding
    class="content"
    :services="services"
    :include-kubernetes-services="settings.portForwarding.includeKubernetesServices"
    :k8s-state="state"
    :kubernetes-is-disabled="!settings.kubernetes.enabled"
    :service-being-edited="serviceBeingEdited"
    :error-message="errorMessage"
    @update-port="handleUpdatePort"
    @toggled-service-filter="onIncludeK8sServicesChanged"
    @edit-port-forward="handleEditPortForward"
    @cancel-port-forward="handleCancelPortForward"
    @cancel-edit-port-forward="handleCancelEditPortForward"
    @update-port-forward="handleUpdatePortForward"
    @close-error="handleCloseError"
  />
</template>

<script lang="ts">

import clone from 'lodash/cloneDeep';
import { defineComponent } from 'vue';

import { State, type ServiceEntry } from '@pkg/backend/k8s';
import PortForwarding from '@pkg/components/PortForwarding.vue';
import { defaultSettings, Settings } from '@pkg/config/settings';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

export default defineComponent({
  name:       'port-forwarding',
  components: { PortForwarding },
  data() {
    return {
      state:              State.STARTED,
      settings:           defaultSettings,
      services:           [] as ServiceEntry[],
      errorMessage:       undefined as string | undefined,
      serviceBeingEdited: undefined as ServiceEntry | undefined,
    };
  },

  watch: {
    services: {
      handler(newServices: ServiceEntry[]): void {
        if (this.serviceBeingEdited) {
          const newService = newServices.find(service => this.compareServices(this.serviceBeingEdited as ServiceEntry, service));

          if (newService) {
            this.serviceBeingEdited = Object.assign(this.serviceBeingEdited, { listenPort: newService.listenPort });
          }
        }
      },
      deep: true,
    },
  },

  mounted() {
    this.$store.dispatch(
      'page/setHeader',
      { title: this.t('portForwarding.title') },
    );
    ipcRenderer.invoke('service-fetch')
      .then((services) => {
        this.$data.services = services;
      });
    ipcRenderer.on('settings-update', (event, settings) => {
      // TODO: put in a status bar
      this.$data.settings = settings;
    });
    ipcRenderer.on('settings-read', (event, settings) => {
      this.$data.settings = settings;
    });
    ipcRenderer.send('settings-read');
  },

  methods: {
    handleUpdatePort(newPort: number): void {
      if (this.serviceBeingEdited) {
        this.serviceBeingEdited.listenPort = newPort;
      }
    },

    onIncludeK8sServicesChanged(value: boolean): void {
      if (value !== this.settings.portForwarding.includeKubernetesServices) {
        ipcRenderer.invoke('settings-write',
          { portForwarding: { includeKubernetesServices: value } } );
      }
    },

    compareServices(service1: ServiceEntry, service2: ServiceEntry): boolean {
      return service1.name === service2.name &&
        service1.namespace === service2.namespace &&
        service1.port === service2.port;
    },

    findServiceMatching(serviceToMatch: ServiceEntry | undefined, serviceList: ServiceEntry[]): ServiceEntry | undefined {
      if (!serviceToMatch) {
        return undefined;
      }
      const compareServices = (service1: ServiceEntry, service2: ServiceEntry) => {
        return service1.name === service2.name &&
          service1.namespace === service2.namespace &&
          service1.port === service2.port;
      };

      return serviceList.find(service => compareServices(service, serviceToMatch));
    },

    handleEditPortForward(service: ServiceEntry): void {
      this.errorMessage = undefined;
      if (this.serviceBeingEdited) {
        ipcRenderer.invoke('service-forward', this.serviceBeingEdited, false);
      }
      this.serviceBeingEdited = Object.assign({}, service);
      // Forward ServiceEntry without listenPort set to get random port.
      // The user can change this after we get a random port.
      ipcRenderer.invoke('service-forward', service, true);
    },

    handleCancelEditPortForward(service: ServiceEntry): void {
      this.errorMessage = undefined;
      ipcRenderer.invoke('service-forward', service, false);
      this.serviceBeingEdited = undefined;
    },

    handleCancelPortForward(service: ServiceEntry): void {
      this.errorMessage = undefined;
      ipcRenderer.invoke('service-forward', service, false);
    },

    handleUpdatePortForward(): void {
      this.errorMessage = undefined;
      if (this.serviceBeingEdited) {
        ipcRenderer.invoke('service-forward', clone(this.serviceBeingEdited), true);
      }
      this.serviceBeingEdited = undefined;
    },

    handleCloseError(): void {
      this.errorMessage = undefined;
    },
  },
});
</script>

<style scoped>
  .content {
    padding-top: 13px;
  }
</style>
