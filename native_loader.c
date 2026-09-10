#include "native_loader.h"

#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

typedef TalonSDKHandle *(*talon_open_fn)(const char *);
typedef void (*talon_close_fn)(TalonSDKHandle *);
typedef int (*talon_persist_fn)(const TalonSDKHandle *);
typedef int (*talon_execute_fn)(const TalonSDKHandle *, const char *, char **);
typedef int (*talon_run_sql_param_bin_fn)(const TalonSDKHandle *, const char *,
                                          const uint8_t *, size_t, uint8_t **,
                                          size_t *);
typedef void (*talon_free_string_fn)(char *);
typedef void (*talon_free_bytes_fn)(uint8_t *, size_t);
typedef const char *(*talon_last_error_fn)(void);
typedef const char *(*talon_last_error_code_fn)(void);
typedef int (*talon_build_manifest_fn)(char **);

static void *native_library;
static talon_open_fn native_open;
static talon_close_fn native_close;
static talon_persist_fn native_persist;
static talon_execute_fn native_execute;
static talon_run_sql_param_bin_fn native_run_sql_param_bin;
static talon_free_string_fn native_free_string;
static talon_free_bytes_fn native_free_bytes;
static talon_last_error_fn native_last_error;
static talon_last_error_code_fn native_last_error_code;
static talon_build_manifest_fn native_build_manifest;

static _Thread_local char loader_error[1024];

static void set_loader_error(const char *prefix, const char *detail) {
    if (detail == NULL) {
        detail = "unknown dynamic-loader error";
    }
    (void)snprintf(loader_error, sizeof(loader_error), "%s: %s", prefix, detail);
}

static void clear_symbols(void) {
    native_open = NULL;
    native_close = NULL;
    native_persist = NULL;
    native_execute = NULL;
    native_run_sql_param_bin = NULL;
    native_free_string = NULL;
    native_free_bytes = NULL;
    native_last_error = NULL;
    native_last_error_code = NULL;
    native_build_manifest = NULL;
}

static void capture_error_code(char *output, size_t output_len) {
    if (output == NULL || output_len == 0) {
        return;
    }
    output[0] = '\0';
    if (native_last_error_code == NULL) {
        return;
    }
    const char *code = native_last_error_code();
    if (code == NULL) {
        return;
    }
    size_t index = 0;
    while (index + 1 < output_len && code[index] != '\0') {
        output[index] = code[index];
        index++;
    }
    output[index] = '\0';
}

static void *required_symbol(void *library, const char *name) {
    (void)dlerror();
    void *symbol = dlsym(library, name);
    const char *error = dlerror();
    if (error != NULL || symbol == NULL) {
        set_loader_error(name, error);
        return NULL;
    }
    return symbol;
}

int talon_sdk_load(const char *library_path) {
    if (native_library != NULL) {
        return 0;
    }
    if (library_path == NULL || library_path[0] == '\0') {
        set_loader_error("dlopen", "library path is empty");
        return -1;
    }

    loader_error[0] = '\0';
    void *library = dlopen(library_path, RTLD_NOW | RTLD_LOCAL);
    if (library == NULL) {
        set_loader_error("dlopen", dlerror());
        return -1;
    }

#define LOAD_REQUIRED(target, symbol_name, function_type)                       \
    do {                                                                         \
        void *address = required_symbol(library, symbol_name);                   \
        if (address == NULL) {                                                   \
            clear_symbols();                                                     \
            (void)dlclose(library);                                              \
            return -1;                                                           \
        }                                                                        \
        *(void **)(&target) = address;                                           \
    } while (0)

    LOAD_REQUIRED(native_open, "talon_open", talon_open_fn);
    LOAD_REQUIRED(native_close, "talon_close", talon_close_fn);
    LOAD_REQUIRED(native_persist, "talon_persist", talon_persist_fn);
    LOAD_REQUIRED(native_execute, "talon_execute", talon_execute_fn);
    LOAD_REQUIRED(native_run_sql_param_bin, "talon_run_sql_param_bin",
                  talon_run_sql_param_bin_fn);
    LOAD_REQUIRED(native_free_string, "talon_free_string", talon_free_string_fn);
    LOAD_REQUIRED(native_free_bytes, "talon_free_bytes", talon_free_bytes_fn);
    LOAD_REQUIRED(native_last_error_code, "talon_last_error_code",
                  talon_last_error_code_fn);
    LOAD_REQUIRED(native_build_manifest, "talon_build_manifest",
                  talon_build_manifest_fn);

#undef LOAD_REQUIRED

    /* Optional diagnostic only. Its text is never used for error classification. */
    (void)dlerror();
    *(void **)(&native_last_error) = dlsym(library, "talon_last_error");
    (void)dlerror();
    native_library = library;
    return 0;
}

void talon_sdk_unload(void) {
    if (native_library != NULL) {
        (void)dlclose(native_library);
    }
    native_library = NULL;
    clear_symbols();
}

const char *talon_sdk_loader_error(void) {
    return loader_error[0] == '\0' ? NULL : loader_error;
}

TalonSDKHandle *talon_sdk_open(const char *path, char *error_code,
                               size_t error_code_len) {
    TalonSDKHandle *handle = native_open == NULL ? NULL : native_open(path);
    if (handle == NULL) {
        capture_error_code(error_code, error_code_len);
    } else if (error_code != NULL && error_code_len > 0) {
        error_code[0] = '\0';
    }
    return handle;
}

void talon_sdk_close(TalonSDKHandle *handle) {
    if (native_close != NULL) {
        native_close(handle);
    }
}

int talon_sdk_persist(const TalonSDKHandle *handle, char *error_code,
                      size_t error_code_len) {
    int result = native_persist == NULL ? -1 : native_persist(handle);
    if (result != 0) {
        capture_error_code(error_code, error_code_len);
    } else if (error_code != NULL && error_code_len > 0) {
        error_code[0] = '\0';
    }
    return result;
}

int talon_sdk_execute(const TalonSDKHandle *handle, const char *cmd_json,
                      char **out_json, char *error_code,
                      size_t error_code_len) {
    int result = native_execute == NULL ? -1 : native_execute(handle, cmd_json, out_json);
    if (result != 0) {
        capture_error_code(error_code, error_code_len);
    } else if (error_code != NULL && error_code_len > 0) {
        error_code[0] = '\0';
    }
    return result;
}

int talon_sdk_run_sql_param_bin(const TalonSDKHandle *handle, const char *sql,
                                const uint8_t *params, size_t params_len,
                                uint8_t **out_data, size_t *out_len,
                                char *error_code, size_t error_code_len) {
    int result = native_run_sql_param_bin == NULL
                     ? -1
                     : native_run_sql_param_bin(handle, sql, params, params_len,
                                                out_data, out_len);
    if (result != 0) {
        capture_error_code(error_code, error_code_len);
    } else if (error_code != NULL && error_code_len > 0) {
        error_code[0] = '\0';
    }
    return result;
}

int talon_sdk_build_manifest(char **out_json, char *error_code,
                             size_t error_code_len) {
    int result = native_build_manifest == NULL ? -1 : native_build_manifest(out_json);
    if (result != 0) {
        capture_error_code(error_code, error_code_len);
    } else if (error_code != NULL && error_code_len > 0) {
        error_code[0] = '\0';
    }
    return result;
}

int talon_sdk_has_symbol(const char *symbol_name) {
    if (native_library == NULL || symbol_name == NULL) {
        return 0;
    }
    (void)dlerror();
    void *symbol = dlsym(native_library, symbol_name);
    const char *error = dlerror();
    return error == NULL && symbol != NULL;
}

int talon_sdk_bounded_strlen(const char *value, size_t max_len,
                             size_t *out_len) {
    if (value == NULL || out_len == NULL) {
        return -1;
    }
    for (size_t index = 0; index <= max_len; index++) {
        if (value[index] == '\0') {
            *out_len = index;
            return 0;
        }
    }
    return -1;
}

void talon_sdk_free_string(char *ptr) {
    if (native_free_string != NULL) {
        native_free_string(ptr);
    }
}

void talon_sdk_free_bytes(uint8_t *ptr, size_t len) {
    if (native_free_bytes != NULL) {
        native_free_bytes(ptr, len);
    }
}
