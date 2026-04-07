import { Plugin } from 'vuex';

import { defineResource, listNamespacedResource, resourceActionName, resourceMutations, resourceState, resourceWatchActions } from '@pkg/store/rddConnection';
import { ActionTree, MutationsType } from '@pkg/store/ts-helpers';
import * as RDDClient from '@rdd-client';

type RDDState = ReturnType<typeof state>;

const resources = [
  defineResource({
    name:       'namespaces',
    path:       () => '/api/v1/namespaces',
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       client => client.listNamespace(),
  }),
  defineResource({
    name:       'configMaps',
    path:       (namespace) => `/api/v1/namespaces/${ namespace }/configmaps`,
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       listNamespacedResource('ConfigMap'),
  }),
  defineResource({
    name:       'systemConfigMaps',
    type:       'ConfigMap',
    path:       () => '/api/v1/namespaces/rdd-system/configmaps',
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       client => client.listNamespacedConfigMap({ namespace: 'rdd-system' }),
  }),
  defineResource({
    name:       'apps',
    path:       () => '/apis/app.rancherdesktop.io/v1alpha1/apps',
    makeClient: config => config.makeApiClient(RDDClient.AppRancherdesktopIoV1alpha1Api),
    list:       client => client.listApp(),
  }),
] as const;

export const state = () => ({
  ...resourceState(resources),
});

export const mutations = {
  ...resourceMutations(resources),
} satisfies MutationsType<RDDState>;

export const actions = {
  ...resourceWatchActions(resources),
} satisfies ActionTree<RDDState, /* root */ any, typeof mutations>;

export const plugins: Plugin<RDDState>[] = [
  function(store) {
    for (const resource of resources) {
      const methodName = resourceActionName('rdd/setupWatch', resource.name);

      store.dispatch(methodName, {
        callback: (error: Error) => {
          console.error(`Error watching ${ resource.name }:`, error);
        },
      }).catch((error: Error) => {
        console.error(`Failed to set up watch for ${ resource.name }:`, error);
      });
    }
  },
];
