// Stub function — returns false, no calls.
bool isFeatureEnabled() {
    return false;
}

// Multi-return function with calls.
int processValue(int x) {
    if (x < 0) {
        logError("negative input");
        return -1;
    }
    auto result = transform(x);
    notify(result);
    return result;
}

// Void function — no returns, has calls.
void initialize() {
    loadConfig();
    setupLogging();
    registerHandlers();
}

// Delegation wrapper — single return with nested call.
std::string formatName(const std::string& first, const std::string& last) {
    return fmt::format("{} {}", first, last);
}

// Method calls and member access.
class Cache {
public:
    bool lookup(const std::string& key) {
        auto it = map_.find(key);
        if (it != map_.end()) {
            stats_.recordHit();
            return true;
        }
        stats_.recordMiss();
        return false;
    }
};
