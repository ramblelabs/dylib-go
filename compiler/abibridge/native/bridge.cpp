// Optional facade over ABIBridge's public C API. No upstream sources are copied.
#include <ABIBridge/SwiftInvocation.h>
#include <algorithm>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <memory>
#include <stdexcept>
#include <string>
#include <vector>

namespace {
constexpr uint32_t max_storage = 1u << 20, max_call_storage = 16u << 20;
using ValueType = std::unique_ptr<ABIValueType, decltype(&ABIReleaseValueType)>;
using Interface = std::unique_ptr<ABISwiftCallInterface, decltype(&ABIReleaseSwiftCallInterface)>;
using Bytes = std::unique_ptr<void, decltype(&std::free)>;

void failure(char *output, uint32_t capacity, const char *message) noexcept {
    if (output && capacity) std::snprintf(output, capacity, "%s", message);
}
[[noreturn]] void invalid(const char *message) { throw std::runtime_error(message); }
void check(const void *value, ABIResolutionFailure *error) {
    if (value) {
        if (error) ABIReleaseResolutionFailure(error);
        return;
    }
    std::unique_ptr<ABIResolutionFailure, decltype(&ABIReleaseResolutionFailure)> owned(error, ABIReleaseResolutionFailure);
    invalid(error ? ABIResolutionFailureMessage(error) : "Native interface construction failed.");
}
struct Reader {
    const unsigned char *next;
    size_t left;
    uint32_t word() {
        if (left < 4) invalid("Truncated signature.");
        uint32_t value;
        std::memcpy(&value, next, 4); next += 4; left -= 4;
        return value;
    }
};
struct Type {
    ValueType native{nullptr, ABIReleaseValueType};
    uint32_t size = 0, alignment = 1;
};
Type type(Reader &reader, bool result) {
    const auto kind = reader.word(), size = reader.word(), alignment = reader.word(), count = reader.word();
    if (count > 64 || size > max_storage || !alignment || alignment > 4096 || (alignment & (alignment-1))) invalid("Invalid storage bounds.");
    ABIResolutionFailure *error = nullptr;
    Type value;
    value.size = size; value.alignment = alignment;
    if (kind <= ABIValuePointer) {
        if (count || (!result && kind == ABIValueVoid)) invalid("Invalid scalar signature.");
        value.native.reset(ABICreateScalarType(kind, &error));
        check(value.native.get(), error);
        if ((kind == ABIValueVoid ? 0 : ABIValueTypeSize(value.native.get())) != size ||
            (kind == ABIValueVoid ? 1 : ABIValueTypeAlignment(value.native.get())) != alignment) invalid("Scalar storage does not match this target.");
    } else if (kind == 13) {
        if (count) invalid("Invalid formally indirect storage.");
        value.native.reset(ABICreateSwiftIndirectStorageType(size, alignment, &error));
        check(value.native.get(), error);
    } else if (kind == 12) {
        if (!size || !count) invalid("Missing Swift scalar components.");
        std::vector<ValueType> owned;
        std::vector<const ABIValueType *> fields;
        std::vector<uint32_t> offsets;
        for (uint32_t i = 0; i < count; ++i) {
            const auto field = reader.word(), offset = reader.word();
            if (field == ABIValueVoid || field > ABIValuePointer) invalid("Invalid Swift scalar component.");
            ValueType native(ABICreateScalarType(field, &error), ABIReleaseValueType);
            check(native.get(), error);
            fields.push_back(native.get()); offsets.push_back(offset); owned.push_back(std::move(native));
        }
        ValueType components(ABICreateStructType(fields.data(), count, &error), ABIReleaseValueType);
        check(components.get(), error);
        for (uint32_t i = 0; i < count; ++i)
            if (ABIValueTypeFieldOffset(components.get(), i) != offsets[i]) invalid("Swift component offsets require an unsupported layout.");
        value.native.reset(ABICreateSwiftStorageType(components.get(), size, alignment, &error));
        check(value.native.get(), error);
    } else invalid("Unknown storage kind.");
    return value;
}
struct Plan {
    Interface native{nullptr, ABIReleaseSwiftCallInterface};
    Type result;
    std::vector<Type> arguments;
    uint32_t argument_size = 0;
};
Bytes storage(uint32_t size, uint32_t alignment) {
    void *value = nullptr;
    const size_t stride = (size_t(size) + alignment - 1) & ~(size_t(alignment) - 1);
    if (size && posix_memalign(&value, std::max(size_t(alignment), sizeof(void *)), stride)) throw std::bad_alloc();
    if (size) std::memset(value, 0, stride);
    return Bytes(value, std::free);
}
}

extern "C" uint32_t dylib_abibridge_version() noexcept { return 3; }

extern "C" void *dylib_abibridge_prepare(const unsigned char *signature, uint32_t size, char *message, uint32_t capacity) noexcept {
    try {
        if (!signature || size > 140000) invalid("Invalid signature buffer.");
        Reader reader{signature, size};
        if (reader.word() != 1) invalid("Unsupported signature version.");
        auto count = reader.word();
        if (count > 256) invalid("Too many Swift arguments.");
        auto plan = std::make_unique<Plan>();
        plan->result = type(reader, true);
        std::vector<const ABIValueType *> parameters;
        uint64_t total = plan->result.size;
        for (uint32_t i = 0; i < count; ++i) {
            auto value = type(reader, false);
            total += value.size;
            if (total > max_call_storage) invalid("Call storage budget exceeded.");
            plan->argument_size += value.size;
            parameters.push_back(value.native.get()); plan->arguments.push_back(std::move(value));
        }
        if (reader.left) invalid("Trailing signature data.");
        ABIResolutionFailure *error = nullptr;
        plan->native.reset(ABICreateSwiftCallInterface(plan->result.native.get(), parameters.data(), count, &error));
        check(plan->native.get(), error);
        return plan.release();
    } catch (const std::exception &error) { failure(message, capacity, error.what()); }
    catch (...) { failure(message, capacity, "Native interface construction failed."); }
    return nullptr;
}

extern "C" int32_t dylib_abibridge_invoke(void *handle, const void *function,
    const unsigned char *arguments, uint32_t argument_size, unsigned char *result, uint32_t result_size,
    char *message, uint32_t capacity) noexcept {
    try {
        auto *plan = static_cast<Plan *>(handle);
        if (!plan || !function || plan->argument_size != argument_size || plan->result.size != result_size ||
            (argument_size && !arguments) || (result_size && !result)) invalid("Invalid Swift invocation buffers.");
        std::vector<Bytes> owned;
        std::vector<void *> pointers;
        size_t offset = 0;
        for (const auto &value : plan->arguments) {
            auto copy = storage(value.size, value.alignment);
            std::memcpy(copy.get(), arguments + offset, value.size);
            offset += value.size; pointers.push_back(copy.get()); owned.push_back(std::move(copy));
        }
        auto output = storage(plan->result.size, plan->result.alignment);
        ABIResolutionFailure *error = nullptr;
        const bool success = ABIUnsafeInvokeSwiftCallInterface(plan->native.get(), ABIUnsafeFunctionAtAddress(function), output.get(), pointers.data(), nullptr, &error);
        check(success ? plan : nullptr, error);
        if (result_size) std::memcpy(result, output.get(), result_size);
        return 0;
    } catch (const std::exception &error) { failure(message, capacity, error.what()); }
    catch (...) { failure(message, capacity, "Native Swift invocation failed."); }
    return 1;
}

extern "C" void dylib_abibridge_release(void *handle) noexcept { delete static_cast<Plan *>(handle); }


namespace {
struct ManagedType {
    const void *metadata;
    ABISwiftValueLayout layout;
};
struct ManagedValue {
    ManagedType *type;
    Bytes bytes{nullptr, std::free};
    bool initialized = false;
    ~ManagedValue() {
        if (initialized) ABISwiftDestroyValue(type->metadata, bytes.get());
    }
};
}

extern "C" void *dylib_abibridge_value_type(uintptr_t provider, uint64_t *layout,
    char *message, uint32_t capacity) noexcept {
    try {
        if (!provider || !layout) invalid("Missing Swift metadata provider.");
        auto metadata = reinterpret_cast<const void *(*)(void)>(provider)();
        if (!metadata) invalid("Swift metadata provider returned null.");
        auto description = ABISwiftGetValueLayout(metadata);
        if (!description.stride || description.size > description.stride || description.stride > max_storage ||
            !description.alignment || description.alignment > 4096 ||
            (description.alignment & (description.alignment-1)) || description.stride % description.alignment)
            invalid("Unsupported Swift value storage.");
        auto type = std::make_unique<ManagedType>();
        type->metadata = metadata; type->layout = description;
        layout[0] = description.size; layout[1] = description.stride; layout[2] = description.alignment;
        return type.release();
    } catch (const std::exception &error) { failure(message, capacity, error.what()); }
    catch (...) { failure(message, capacity, "Swift metadata preparation failed."); }
    return nullptr;
}
extern "C" void dylib_abibridge_value_type_release(void *handle) noexcept {
    delete static_cast<ManagedType *>(handle);
}
extern "C" void *dylib_abibridge_value_new(void *handle, const void *source,
    char *message, uint32_t capacity) noexcept {
    try {
        auto *type = static_cast<ManagedType *>(handle);
        if (!type || (source && uintptr_t(source) % type->layout.alignment)) invalid("Invalid Swift value source.");
        auto value = std::make_unique<ManagedValue>();
        value->type = type;
        void *bytes = nullptr;
        if (posix_memalign(&bytes, std::max(type->layout.alignment, sizeof(void *)), type->layout.stride)) throw std::bad_alloc();
        value->bytes.reset(bytes);
        std::memset(bytes, 0, type->layout.stride);
        if (source) {
            ABISwiftCopyValue(type->metadata, bytes, source);
            value->initialized = true;
        }
        return value.release();
    } catch (const std::exception &error) { failure(message, capacity, error.what()); }
    catch (...) { failure(message, capacity, "Swift value allocation failed."); }
    return nullptr;
}
extern "C" uintptr_t dylib_abibridge_value_pointer(void *handle) noexcept {
    return uintptr_t(static_cast<ManagedValue *>(handle)->bytes.get());
}
extern "C" void dylib_abibridge_value_commit(void *handle) noexcept {
    static_cast<ManagedValue *>(handle)->initialized = true;
}
extern "C" void dylib_abibridge_value_release(void *handle) noexcept {
    delete static_cast<ManagedValue *>(handle);
}

extern "C" int32_t dylib_abibridge_invoke_values(void *handle, const void *function,
    const unsigned char *arguments, uint32_t argument_size, unsigned char *result, uint32_t result_size,
    const uint64_t *handles, uint32_t count, char *message, uint32_t capacity) noexcept {
    try {
        auto *plan = static_cast<Plan *>(handle);
        if (!plan || !function || !handles || count != plan->arguments.size() + 1 ||
            (argument_size && !arguments) || (result_size && !result)) invalid("Invalid managed invocation buffers.");
        auto managed = [](uint64_t handle, const Type &type, bool initialized) -> ManagedValue * {
            auto *value = reinterpret_cast<ManagedValue *>(uintptr_t(handle));
            if (value && (value->initialized != initialized || value->type->layout.size != type.size ||
                value->type->layout.alignment != type.alignment)) invalid("Swift value does not match its invocation storage.");
            return value;
        };
        auto *output = managed(handles[0], plan->result, false);
        if (result_size != (output ? 0 : plan->result.size)) invalid("Invalid Swift result byte count.");
        std::vector<Bytes> owned;
        std::vector<void *> pointers;
        size_t offset = 0;
        for (size_t i = 0; i < plan->arguments.size(); ++i) {
            const auto &type = plan->arguments[i];
            if (auto *value = managed(handles[i+1], type, true)) {
                pointers.push_back(value->bytes.get());
            } else {
                if (offset > argument_size || type.size > argument_size - offset) invalid("Invalid Swift argument byte count.");
                auto copy = storage(type.size, type.alignment);
                if (type.size) std::memcpy(copy.get(), arguments + offset, type.size);
                offset += type.size;
                pointers.push_back(copy.get()); owned.push_back(std::move(copy));
            }
        }
        if (offset != argument_size) invalid("Trailing Swift argument bytes.");
        auto bytes = output ? Bytes(nullptr, std::free) : storage(plan->result.size, plan->result.alignment);
        ABIResolutionFailure *error = nullptr;
        const bool success = ABIUnsafeInvokeSwiftCallInterface(plan->native.get(), ABIUnsafeFunctionAtAddress(function),
            output ? output->bytes.get() : bytes.get(), pointers.data(), nullptr, &error);
        check(success ? plan : nullptr, error);
        // Swift returned an owned result directly into uninitialized storage.
        // No representation copy or additional retain changes that ownership.
        if (output) output->initialized = true;
        else if (result_size) std::memcpy(result, bytes.get(), result_size);
        return 0;
    } catch (const std::exception &error) { failure(message, capacity, error.what()); }
    catch (...) { failure(message, capacity, "Managed Swift invocation failed."); }
    return 1;
}
