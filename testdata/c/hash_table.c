#include <stdlib.h>
#include <string.h>
#include "hash_table.h"

/* FNV-1a hash function for strings. */
static unsigned long fnv1a_hash(const char *key) {
    unsigned long hash = 2166136261UL;
    while (*key) {
        hash ^= (unsigned char)*key++;
        hash *= 16777619UL;
    }
    return hash;
}

/* Find the bucket index for a key. */
static size_t bucket_index(const HashTable *table, const char *key) {
    return fnv1a_hash(key) % table->capacity;
}

HashTable *hash_table_create(size_t capacity) {
    HashTable *table = malloc(sizeof(HashTable));
    if (!table) return NULL;

    table->buckets = calloc(capacity, sizeof(HashEntry *));
    if (!table->buckets) {
        free(table);
        return NULL;
    }

    table->capacity = capacity;
    table->size = 0;
    table->load_factor_threshold = 0.75f;
    return table;
}

/**
 * Resizes the hash table when the load factor exceeds the threshold.
 */
static bool hash_table_resize(HashTable *table) {
    size_t new_capacity = table->capacity * 2;
    HashEntry **new_buckets = calloc(new_capacity, sizeof(HashEntry *));
    if (!new_buckets) return false;

    /* Rehash all entries. */
    for (size_t i = 0; i < table->capacity; i++) {
        HashEntry *entry = table->buckets[i];
        while (entry) {
            HashEntry *next = entry->next;
            size_t idx = fnv1a_hash(entry->key) % new_capacity;
            entry->next = new_buckets[idx];
            new_buckets[idx] = entry;
            entry = next;
        }
    }

    free(table->buckets);
    table->buckets = new_buckets;
    table->capacity = new_capacity;
    return true;
}

bool hash_table_insert(HashTable *table, const char *key, void *value) {
    /* Check load factor and resize if needed. */
    if ((float)table->size / (float)table->capacity > table->load_factor_threshold) {
        if (!hash_table_resize(table)) return false;
    }

    size_t idx = bucket_index(table, key);
    HashEntry *entry = table->buckets[idx];

    /* Update existing key. */
    while (entry) {
        if (strcmp(entry->key, key) == 0) {
            entry->value = value;
            return true;
        }
        entry = entry->next;
    }

    /* Insert new entry at head of chain. */
    HashEntry *new_entry = malloc(sizeof(HashEntry));
    if (!new_entry) return false;

    new_entry->key = strdup(key);
    if (!new_entry->key) {
        free(new_entry);
        return false;
    }

    new_entry->value = value;
    new_entry->next = table->buckets[idx];
    table->buckets[idx] = new_entry;
    table->size++;
    return true;
}

void *hash_table_get(const HashTable *table, const char *key) {
    size_t idx = bucket_index(table, key);
    HashEntry *entry = table->buckets[idx];

    while (entry) {
        if (strcmp(entry->key, key) == 0) {
            return entry->value;
        }
        entry = entry->next;
    }
    return NULL;
}

bool hash_table_remove(HashTable *table, const char *key) {
    size_t idx = bucket_index(table, key);
    HashEntry *entry = table->buckets[idx];
    HashEntry *prev = NULL;

    while (entry) {
        if (strcmp(entry->key, key) == 0) {
            if (prev) {
                prev->next = entry->next;
            } else {
                table->buckets[idx] = entry->next;
            }
            free(entry->key);
            free(entry);
            table->size--;
            return true;
        }
        prev = entry;
        entry = entry->next;
    }
    return false;
}

size_t hash_table_size(const HashTable *table) {
    return table->size;
}

void hash_table_destroy(HashTable *table) {
    for (size_t i = 0; i < table->capacity; i++) {
        HashEntry *entry = table->buckets[i];
        while (entry) {
            HashEntry *next = entry->next;
            free(entry->key);
            free(entry);
            entry = next;
        }
    }
    free(table->buckets);
    free(table);
}
