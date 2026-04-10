#ifndef CONTAINER_HPP
#define CONTAINER_HPP

#include <vector>
#include <string>

namespace testlib {
namespace collections {

/**
 * A generic container with basic operations.
 */
template<typename T>
class Container {
public:
    Container();
    explicit Container(size_t capacity);
    ~Container();

    void add(const T& item);
    T get(size_t index) const;
    size_t size() const { return items_.size(); }
    bool empty() const;

private:
    std::vector<T> items_;
    size_t capacity_;
};

/**
 * Metadata about a file in the index.
 */
struct FileEntry {
    std::string path;
    size_t size;
    bool indexed;

    void markIndexed();
    std::string displayName() const;
};

/**
 * Status codes for operations.
 */
enum class Status {
    Ok,
    NotFound,
    PermissionDenied,
    IoError
};

} // namespace collections
} // namespace testlib

#endif // CONTAINER_HPP
