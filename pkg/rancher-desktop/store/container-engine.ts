import { defineResource, listNamespacedResource, ListResourceOptions, resourceMutations, resourceState, resourceWatchActions } from '@pkg/store/rddConnection';
import { ActionTree, MutationsType } from '@pkg/store/ts-helpers';
import * as RDDClient from '@rdd-client';

type ContainerEngineState = ReturnType<typeof state>;

function listContainerNamespacedResource<
  TypeName extends string,
  C extends Record<
    `listNamespaced${ TypeName }`,
    (param: any, options?: RDDClient.ConfigurationOptions) => any
  >,
>(typeName: TypeName) {
  return (client: C, untypedOptions: ListResourceOptions<any>): ReturnType<C[`listNamespaced${ TypeName }`]> => {
    const options: ListResourceOptions<ContainerEngineState> = untypedOptions;
    const request: Parameters<C[`listNamespaced${ TypeName }`]>[0] = {
      namespace: options.connectionState.namespace,
    };

    if (options.state.currentNamespace) {
      request.fieldSelector = `status.namespace=${ options.state.currentNamespace }`;
    }

    return client[`listNamespaced${ typeName }`](request);
  };
}

const resources = [
  defineResource({
    name:       'namespaces',
    type:       'containerNamespace',
    path:       '/apis/containers.rancherdesktop.io/v1alpha1/containernamespaces',
    makeClient: config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    list:       listNamespacedResource('ContainerNamespace'),
  }),
  defineResource({
    name:       'volumes',
    path:       '/apis/containers.rancherdesktop.io/v1alpha1/volumes',
    makeClient: config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    list:       listContainerNamespacedResource('Volume'),
  }),
] as const;

type errorSource = 'volumes' | 'namespaces';

export const state = () => ({
  ...resourceState(resources),
  currentNamespace: undefined as string | undefined,
  error:            undefined as undefined | { error: Error, source: errorSource },
});

export const getters = ({
  supportsNamespaces(): boolean {
    // TODO: Determine if the backend supports namespaces.
    return false;
  },
});

export const mutations = {
  ...resourceMutations(resources),
  SET_CURRENT_NAMESPACE(state, namespace) {
    state.currentNamespace = namespace;
  },
  SET_ERROR(state, payload?) {
    state.error = payload;
  },
} satisfies MutationsType<ContainerEngineState>;

export const actions = {
  ...resourceWatchActions(resources),
  async volumeDelete(
    { rootState, commit },
    { volume }: {
      volume: RDDClient.IoRancherdesktopContainersV1alpha1Volume,
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    try {
      const status = await client.deleteNamespacedVolume({
        name:      volume.metadata!.name!,
        namespace: volume.metadata!.namespace!,
      });
      if (status.status !== 'Success') {
        commit('SET_ERROR', {
          error:  new Error(`Failed to delete volume ${ volume.metadata!.name }: ${ status.message }`),
          source: 'volumes',
        });
      }
    } catch (error: any) {
      commit('SET_ERROR', { error, source: 'volumes' });
    }
  },
} satisfies ActionTree<ContainerEngineState, any, typeof mutations>;
