#ifndef TALON_SDK_NATIVE_LOADER_H
#define TALON_SDK_NATIVE_LOADER_H

#include <stddef.h>
#include <stdint.h>

typedef struct TalonSDKHandle TalonSDKHandle;

int talon_sdk_load(const char *library_path);
void talon_sdk_unload(void);
const char *talon_sdk_loader_error(void);

TalonSDKHandle *talon_sdk_open(const char *path,
                               char *error_code,
                               size_t error_code_len);
void talon_sdk_close(TalonSDKHandle *handle);
int talon_sdk_persist(const TalonSDKHandle *handle,
                      char *error_code,
                      size_t error_code_len);
int talon_sdk_execute(const TalonSDKHandle *handle,
                      const char *cmd_json,
                      char **out_json,
                      char *error_code,
                      size_t error_code_len);
int talon_sdk_run_sql_param_bin(const TalonSDKHandle *handle,
                                const char *sql,
                                const uint8_t *params,
                                size_t params_len,
                                uint8_t **out_data,
                                size_t *out_len,
                                char *error_code,
                                size_t error_code_len);
int talon_sdk_build_manifest(char **out_json,
                             char *error_code,
                             size_t error_code_len);
int talon_sdk_has_symbol(const char *symbol_name);
int talon_sdk_bounded_strlen(const char *value,
                             size_t max_len,
                             size_t *out_len);
void talon_sdk_free_string(char *ptr);
void talon_sdk_free_bytes(uint8_t *ptr, size_t len);

#endif
