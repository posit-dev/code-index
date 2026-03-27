#include <string>
#include <unordered_map>
#include <vector>
#include <mutex>
#include <optional>

/**
 * A thread-safe interning pool for strings.
 *
 * Stores each unique string exactly once and returns a lightweight
 * handle (uint32_t) for comparisons and lookups. Useful for reducing
 * memory when many duplicate strings are expected (e.g., package names,
 * version strings).
 */
class StringPool {
public:
    using Handle = uint32_t;

    /**
     * Returns the singleton instance of the string pool.
     */
    static StringPool& instance() {
        static StringPool pool;
        return pool;
    }

    /**
     * Interns a string and returns its unique handle.
     * If the string was already interned, returns the existing handle.
     *
     * Thread-safe.
     */
    Handle intern(const std::string& value) {
        std::lock_guard<std::mutex> lock(mutex_);
        auto it = index_.find(value);
        if (it != index_.end()) {
            return it->second;
        }
        Handle handle = static_cast<Handle>(strings_.size());
        strings_.push_back(value);
        index_[value] = handle;
        return handle;
    }

    /**
     * Resolves a handle back to its string value.
     * Returns std::nullopt if the handle is invalid.
     */
    std::optional<std::string> resolve(Handle handle) const {
        std::lock_guard<std::mutex> lock(mutex_);
        if (handle < strings_.size()) {
            return strings_[handle];
        }
        return std::nullopt;
    }

    /**
     * Returns the number of unique strings in the pool.
     */
    size_t size() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return strings_.size();
    }

    /**
     * Clears all interned strings. Existing handles become invalid.
     */
    void clear() {
        std::lock_guard<std::mutex> lock(mutex_);
        strings_.clear();
        index_.clear();
    }

private:
    StringPool() = default;
    StringPool(const StringPool&) = delete;
    StringPool& operator=(const StringPool&) = delete;

    mutable std::mutex mutex_;
    std::vector<std::string> strings_;
    std::unordered_map<std::string, Handle> index_;
};

/**
 * Compares two string pool handles for equality without
 * dereferencing the actual strings.
 */
bool handles_equal(StringPool::Handle a, StringPool::Handle b) {
    return a == b;
}

/**
 * Batch-interns a vector of strings and returns their handles.
 */
std::vector<StringPool::Handle> batch_intern(
    StringPool& pool,
    const std::vector<std::string>& values
) {
    std::vector<StringPool::Handle> handles;
    handles.reserve(values.size());
    for (const auto& v : values) {
        handles.push_back(pool.intern(v));
    }
    return handles;
}
