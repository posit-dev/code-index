/**
 * @file hash_table.h
 * @brief A simple chaining hash table implementation.
 */

#ifndef HASH_TABLE_H
#define HASH_TABLE_H

#include <stddef.h>
#include <stdbool.h>

/**
 * A single key-value entry in the hash table.
 */
typedef struct HashEntry {
    char *key;
    void *value;
    struct HashEntry *next;
} HashEntry;

/**
 * A hash table using separate chaining for collision resolution.
 */
typedef struct HashTable {
    HashEntry **buckets;
    size_t capacity;
    size_t size;
    float load_factor_threshold;
} HashTable;

/**
 * Creates a new hash table with the given initial capacity.
 *
 * @param capacity Initial number of buckets.
 * @return A pointer to the new hash table, or NULL on allocation failure.
 */
HashTable *hash_table_create(size_t capacity);

/**
 * Inserts a key-value pair into the hash table.
 * If the key already exists, the value is updated.
 *
 * @param table The hash table.
 * @param key The key string (will be copied).
 * @param value The value pointer.
 * @return true if insertion succeeded, false on allocation failure.
 */
bool hash_table_insert(HashTable *table, const char *key, void *value);

/**
 * Retrieves the value associated with a key.
 *
 * @param table The hash table.
 * @param key The key to look up.
 * @return The value pointer, or NULL if the key is not found.
 */
void *hash_table_get(const HashTable *table, const char *key);

/**
 * Removes a key-value pair from the hash table.
 *
 * @param table The hash table.
 * @param key The key to remove.
 * @return true if the key was found and removed.
 */
bool hash_table_remove(HashTable *table, const char *key);

/**
 * Returns the number of entries in the hash table.
 */
size_t hash_table_size(const HashTable *table);

/**
 * Frees all memory associated with the hash table.
 * Does not free the stored values.
 */
void hash_table_destroy(HashTable *table);

#endif /* HASH_TABLE_H */
