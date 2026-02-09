import { State as EngineStates } from '@pkg/backend/k8s';

export const state = () => ({ k8sState: EngineStates.DISABLED });

export const mutations = {
  SET_K8S_STATE(state, k8sState) {
    state.k8sState = k8sState;
  },
};

export const actions = {
  setK8sState({ commit }, k8sState) {
    commit('SET_K8S_STATE', k8sState);
  },
};

export const getters = {
  getK8sState({ k8sState }) {
    return k8sState;
  },
  isReady({ k8sState }) {
    return [EngineStates.STARTED, EngineStates.DISABLED].includes(k8sState);
  },
};
