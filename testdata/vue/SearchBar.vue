<script setup lang="ts">
import { ref, computed, watch } from "vue";

/**
 * Props for the SearchBar component.
 */
interface Props {
  placeholder?: string;
  debounceMs?: number;
  maxResults?: number;
}

const props = withDefaults(defineProps<Props>(), {
  placeholder: "Search...",
  debounceMs: 300,
  maxResults: 10,
});

const emit = defineEmits<{
  (e: "search", query: string): void;
  (e: "clear"): void;
}>();

const query = ref("");
const isLoading = ref(false);

/** Computed property that returns true if the search input has content. */
const hasQuery = computed(() => query.value.trim().length > 0);

let debounceTimer: ReturnType<typeof setTimeout> | null = null;

/** Debounces the search input and emits the search event. */
function handleInput(event: Event) {
  const value = (event.target as HTMLInputElement).value;
  query.value = value;

  if (debounceTimer) clearTimeout(debounceTimer);

  debounceTimer = setTimeout(() => {
    if (value.trim()) {
      emit("search", value.trim());
    }
  }, props.debounceMs);
}

/** Clears the search input and emits the clear event. */
function handleClear() {
  query.value = "";
  if (debounceTimer) clearTimeout(debounceTimer);
  emit("clear");
}

/** Formats a result count for display. */
function formatResultCount(count: number): string {
  if (count === 0) return "No results";
  if (count === 1) return "1 result";
  return `${count} results`;
}
</script>

<template>
  <div class="search-bar">
    <input
      :value="query"
      :placeholder="placeholder"
      @input="handleInput"
      type="text"
    />
    <button v-if="hasQuery" @click="handleClear">Clear</button>
  </div>
</template>
