<template>
  <div>
    <RouterView />
    <Images
      class="content"
      data-test="imagesTable"
      :images="images"
      :image-namespaces="imageNamespaces"
      :state="state"
      :show-all="settings.images.showAll"
      :selected-namespace="settings.images.namespace"
      :supports-namespaces="supportsNamespaces"
      :protected-images="protectedImages"
      @toggled-show-all="onShowAllImagesChanged"
      @switch-namespace="onChangeNamespace"
    />
  </div>
</template>

<script lang="ts">

import _ from 'lodash';
import { defineComponent } from 'vue';

import { State as K8sState } from '@pkg/backend/backend';
import Images from '@pkg/components/Images.vue';
import { defaultSettings } from '@pkg/config/settings';
import { mapTypedActions, mapTypedGetters, mapTypedMutations, mapTypedState } from '@pkg/entry/store';
import { IpcRendererEvents } from '@pkg/typings/electron-ipc';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

interface Image {
  imageName: string;
  tag:       string;
  imageID:   string;
}

enum ImageManagerStates {
  UNREADY = 'IMAGE_MANAGER_UNREADY',
  READY = 'READY',
}

export default defineComponent({
  components: { Images },
  data() {
    return {
      settings:           defaultSettings,
      images:             [] as Image[],
      imageNamespaces:    [] as string[],
      supportsNamespaces: true,
    };
  },

  computed: {
    state() {
      if ((window as any).imagesListMock) {
        // Override for screenshots
        return ImageManagerStates.READY;
      }

      return this.imageManagerState ? ImageManagerStates.READY : ImageManagerStates.UNREADY;
    },
    rancherImages(): string[] {
      return this.images
        .map(image => image.imageName)
        .filter(name => name.startsWith('rancher/'));
    },
    installedExtensionImages(): string[] {
      return this.installedExtensions.map(image => image.id);
    },
    protectedImages(): string[] {
      return [
        'moby/buildkit',
        'ghcr.io/rancher-sandbox/rancher-desktop/rdx-proxy',
        ...this.rancherImages,
        ...this.installedExtensionImages,
      ];
    },
    ...mapTypedState('imageManager', ['imageManagerState']),
    ...mapTypedGetters('extensions', ['installedExtensions']),
  },

  watch: {
    state: {
      handler(state: string) {
        this.setHeader({ title: this.t('images.title') });

        if (!state || state === ImageManagerStates.UNREADY) {
          return;
        }

        this.setAction({ action: 'ImagesButtonAdd' });
      },
      immediate: true,
    },
  },

  mounted() {
    ipcRenderer.on('settings-update', (event, settings) => {
      // TODO: put in a status bar
      this.$data.settings = settings;
      this.checkSelectedNamespace();
    });
    ipcRenderer.on('settings-read', (event, settings) => {
      this.settings = settings;
    });
    ipcRenderer.send('settings-read');

    ipcRenderer.on('extensions/changed', this.fetchExtensions);
    this.fetchExtensions();
  },
  beforeUnmount() {
    ipcRenderer.removeListener('extensions/changed', this.fetchExtensions);
  },

  methods: {
    ...mapTypedActions('extensions', { fetchExtensions: 'fetch' }),
    ...mapTypedActions('page', ['setAction', 'setHeader']),
    ...mapTypedMutations('imageManager', { setImageManagerState: 'SET_IMAGE_MANAGER_STATE' }),
    checkSelectedNamespace() {
      if (!this.supportsNamespaces || this.imageNamespaces.length === 0) {
        // Nothing to verify yet
        return;
      }
      if (!this.imageNamespaces.includes(this.settings.images.namespace)) {
        const defaultNamespace = this.imageNamespaces.includes('default') ? 'default' : this.imageNamespaces[0];

        ipcRenderer.invoke('settings-write',
          { images: { namespace: defaultNamespace } } );
      }
    },
    onShowAllImagesChanged(value: boolean) {
      if (value !== this.settings.images.showAll) {
        ipcRenderer.invoke('settings-write',
          { images: { showAll: value } } );
      }
    },
    onChangeNamespace(value: string) {
      if (value !== this.settings.images.namespace) {
        ipcRenderer.invoke('settings-write',
          { images: { namespace: value } } );
      }
    },
  },
});
</script>
