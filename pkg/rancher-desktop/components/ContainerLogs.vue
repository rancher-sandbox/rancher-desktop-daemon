<template>
  <div class="container-logs-component">
    <banner
      v-if="errorMessage"
      class="content-state"
      color="error"
      data-testid="error-message"
    >
      <span class="icon icon-info-circle icon-lg" />
      {{ errorMessage }}
    </banner>

    <loading-indicator
      v-if="!container || waitingForInitialLogs"
      class="content-state"
      data-testid="loading-indicator"
    >
      Loading logs...
    </loading-indicator>

    <div
      v-else
      ref="terminalContainer"
      :class="['terminal-container']"
      data-testid="terminal"
    />
  </div>
</template>

<script lang="ts" setup>
import { Banner } from '@rancher/components';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal } from '@xterm/xterm';
import { shell } from 'electron';
import { ref, onMounted, onBeforeUnmount, watch, nextTick, useTemplateRef, computed } from 'vue';
import { useStore } from 'vuex';

import LoadingIndicator from '@pkg/components/LoadingIndicator.vue';
import { usePassthroughURL } from '@pkg/composables/passthrough';

defineOptions({ name: 'ContainerLogs' });

const { containerId } = defineProps<{
  containerId: string;
}>();

defineExpose({
  clearSearch,
  performSearch,
  searchNext,
  searchPrevious,
});

const store = useStore();
const errorMessage = ref<string | undefined>();

let terminal: Terminal | undefined;
let fitAddon: FitAddon | undefined;
let searchAddon: SearchAddon | undefined;
let searchDebounceTimer: ReturnType<typeof setTimeout> | undefined;

let terminalAborter: AbortController | undefined;
let buffer = '';
/**
 * waitingForInitialLogs is used to delay showing the terminal until after the
 * initial logs have streamed in.  This prevents the user seeing a quick stream
 * of logs coming in initially.
 */
const waitingForInitialLogs = ref(true);
const container = computed(() => {
  const containers = store.state['container-engine'].containers;

  return containers?.find(c => c.metadata?.name === containerId);
});

/**
 * Timer used with `waitingForInitialLogs` to reveal the terminal after a delay.
 */
let revealTimeout: ReturnType<typeof setTimeout> | undefined;

const genericLogURL = usePassthroughURL('logs');
const logURL = computed(() => {
  if (!genericLogURL.value || !container.value?.metadata?.name) {
    return undefined;
  }
  return new URL(container.value.metadata.name, genericLogURL.value);
});

const terminalContainer = useTemplateRef('terminalContainer');
// Hook up the terminal once the container is mounted.
watch(terminalContainer, async(terminalContainer, _oldValue, onCleanup) => {
  terminalAborter?.abort();
  terminalAborter = undefined;

  if (!terminalContainer) {
    return;
  }

  terminalAborter = new AbortController();
  onCleanup(() => terminalAborter?.abort());

  const t = new Terminal({
    theme: {
      background:          '#1a1a1a',
      foreground:          '#e0e0e0',
      cursor:              '#1a1a1a', // same as the background to effectively hide the cursor.
      black:               '#000000',
      red:                 '#ff5555',
      green:               '#50fa7b',
      yellow:              '#f1fa8c',
      blue:                '#8be9fd',
      magenta:             '#ff79c6',
      cyan:                '#8be9fd',
      white:               '#f8f8f2',
      brightBlack:         '#6272a4',
      brightRed:           '#ff6e6e',
      brightGreen:         '#69ff94',
      brightYellow:        '#ffffa5',
      brightBlue:          '#d6acff',
      brightMagenta:       '#ff92df',
      brightCyan:          '#a4ffff',
      brightWhite:         '#ffffff',
    },
    fontSize:     14,
    fontFamily:   '"Courier New", "Monaco", monospace',
    cursorBlink:  false,
    disableStdin: true,
    convertEol:   true,
    scrollback:   50_000,
  });
  terminalAborter.signal.addEventListener('abort', () => {
    terminal = undefined;
    t.dispose();
  });

  fitAddon = new FitAddon();
  t.loadAddon(fitAddon);

  searchAddon = new SearchAddon();
  t.loadAddon(searchAddon);
  terminalAborter.signal.addEventListener('abort', () => {
    searchAddon?.dispose();
    searchAddon = undefined;
  });

  t.loadAddon(new WebLinksAddon((event: MouseEvent, uri: string) => {
    event.preventDefault();
    shell.openExternal(uri);
  }));

  // Disable key events to allow normal behaviour such as copy/paste.
  t.attachCustomKeyEventHandler(() => false);
  terminal = t;
  t.open(terminalContainer);

  await nextTick();
  if (buffer) {
    // If we have data streamed in before the terminal showed up, write it now.
    // This shouldn't be necessary as we show the terminal as soon as the socket
    // opens, but it's still possible to end up with a race if the first message
    // comes in before the terminal container watch fires.
    t.write(buffer);
    buffer = '';
  }

  const resizeObserver = new ResizeObserver(fitAddon.fit.bind(fitAddon));
  resizeObserver.observe(terminalContainer);
  terminalAborter.signal.addEventListener('abort', () => {
    resizeObserver.disconnect();
  });
  fitAddon.fit();
});

// Start streaming logs from the container.
watch([logURL, container], async([logURL, container], _, cleanUp) => {
  if (!container || !logURL) {
    return;
  }
  const streamAborter = new AbortController();
  cleanUp(() => streamAborter.abort());
  streamAborter.signal.addEventListener('abort', () => {
    buffer = '';
    terminal?.reset();
  });

  // Wait a bit for the rendering to finish, so we don't end up creating many
  // connections before Vue finished its thing.  `nextTick` still causes
  // spurious events to fire, so use a longer timeout here.
  await new Promise(resolve => setTimeout(resolve, 200));
  if (streamAborter.signal.aborted) {
    return;
  }

  try {
    buffer = '';

    // Authentication for the log stream is handled in the main process via the
    // webRequest.onBeforeSendHeaders listener; see `@pkg/window/index.ts`.
    const socket = new WebSocket(logURL);
    streamAborter.signal.addEventListener('abort', () => socket.close());
    socket.addEventListener('message', (event) => {
      if (typeof event.data !== 'string') {
        return;
      }
      if (streamAborter.signal.aborted) {
        // We can get here if we closed the connection at the WebSocket level
        // (i.e. sent a "close" message), but the server hasn't paid attention
        // to it yet and kept sending things to us.
        socket.close();
        return;
      }
      const t = terminal;
      if (!t) {
        buffer += event.data;
        return;
      }
      errorMessage.value = undefined;
      t.write(event.data);
      if (waitingForInitialLogs.value) {
        // If we're still waiting for the initial logs, delay the reveal
        // until after all of the initial logs have streamed in.
        clearTimeout(revealTimeout);

        revealTimeout = setTimeout(() => {
          waitingForInitialLogs.value = false;
          nextTick(() => {
            fitAddon?.fit();
            terminal?.scrollToBottom();
          });
        }, 200);
      }
    });
    socket.addEventListener('open', () => {
      // Once the connection is open, reset the initial wait to avoid the logs
      // streaming in quickly.  Do not do the reset earlier, so that the user
      // can still look at the last known logs during a disconnect.
      waitingForInitialLogs.value = true;
      clearTimeout(revealTimeout);

      revealTimeout = setTimeout(() => {
        waitingForInitialLogs.value = false;
        nextTick(() => {
          fitAddon?.fit();
          terminal?.scrollToBottom();
        });
      }, 500);
    });
    socket.addEventListener('error', (event) => {
      console.error('WebSocket error:', event);
      errorMessage.value = `WebSocket error: ${ 'message' in event ? event.message : event }`;
    });
  } catch (err: any) {
    console.error('Error setting up log stream:', err);
    errorMessage.value = `Failed to load logs: ${ err.message || err }`;
  }
});

function clearSearch() {
  searchAddon?.clearDecorations();
}

function performSearch(searchTerm: string) {
  clearTimeout(searchDebounceTimer);

  searchDebounceTimer = setTimeout(() => {
    if (!searchAddon) return;

    searchAddon.clearDecorations();
    if (searchTerm) {
      try {
        searchAddon.findNext(searchTerm);
      } catch (err) {
        console.error('Search error:', err);
      }
    }
  }, 300);
}

function searchNext(searchTerm: string) {
  if (!searchAddon || !searchTerm) return;
  executeSearch(() => searchAddon?.findNext(searchTerm));
}

function searchPrevious(searchTerm: string) {
  if (!searchAddon || !searchTerm) return;
  executeSearch(() => searchAddon?.findPrevious(searchTerm));
}

function executeSearch(searchFn: () => void) {
  try {
    searchFn();
  } catch (err) {
    console.error('Search error:', err);
  }
}

onMounted(() => {
  store.dispatch('container-engine/watchContainers').catch(console.error);
});

onBeforeUnmount(() => {
  terminalAborter?.abort();
  clearTimeout(searchDebounceTimer);
  clearTimeout(revealTimeout);
});
</script>

<style lang="scss" scoped>
@import '@xterm/xterm/css/xterm.css';

.container-logs-component {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
  flex: 1;
}

.content-state {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 2.5rem;
}

.terminal-container {
  background: #1a1a1a;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;

  &.terminal-hidden {
    visibility: hidden;
  }

  :deep(.xterm) {
    height: 100%;
  }

  :deep(.xterm-selection) {
    overflow: hidden;
  }
}
</style>
