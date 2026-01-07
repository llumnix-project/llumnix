use std::{
    ffi::{CStr, CString},
    sync::Arc,
};

use std::ptr;

use crate::traits;
use crate::HuggingFaceTokenizer;
use crate::LlamaTokenizer;
use crate::{create_tokenizer_from_file, create_tokenizer_with_chat_template};

#[cfg(target_os = "macos")]
type boolean_t = libc::boolean_t;
#[cfg(not(target_os = "macos"))]
type boolean_t = libc::c_int;


#[repr(C)]
pub struct TokenizersBuffer {
    ids: *mut u32,
    // type_ids: *mut u32,
    // special_tokens_mask: *mut u32,
    // attention_mask: *mut u32,
    // tokens: *mut *mut libc::c_char,
    // offsets: *mut usize,
    len: usize,
}

#[no_mangle]
pub extern "C" fn tokenizers_from_file_with_chat_template(
    model_dir: *const libc::c_char,
    chat_template_path: *const libc::c_char,
    error: *mut *mut libc::c_char,
) -> *mut libc::c_void {
    if model_dir.is_null() {
        if !error.is_null() {
            let err_msg = CString::new("Tokenizer file path is null").unwrap();
            unsafe {
                *error = err_msg.into_raw();
            }
        }
        return ptr::null_mut();
    }

    let model_dir_cstr = unsafe { CStr::from_ptr(model_dir) };
    let model_dir_str = match model_dir_cstr.to_str() {
        Ok(s) => s,
        Err(e) => {
            if !error.is_null() {
                let err_msg =
                    CString::new(format!("Invalid UTF-8 in model_dir path: {}", e)).unwrap();
                unsafe {
                    *error = err_msg.into_raw();
                }
            }
            return ptr::null_mut();
        }
    };

    if chat_template_path.is_null() {
        let tokenizer = create_tokenizer_from_file(model_dir_str);
        match tokenizer {
            Ok(tokenizer) => {
                return Box::into_raw(Box::new(tokenizer)).cast();
            }
            Err(e) => {
                if !error.is_null() {
                    let err_msg =
                        CString::new(format!("Failed to create tokenizer: {}", e)).unwrap();
                    unsafe {
                        *error = err_msg.into_raw();
                    }
                }
                return ptr::null_mut();
            }
        }
    } else {
        let chat_template_path_cstr = unsafe { CStr::from_ptr(chat_template_path) };
        let chat_template_path_str = match chat_template_path_cstr.to_str() {
            Ok(s) => s,
            Err(e) => {
                if !error.is_null() {
                    let err_msg =
                        CString::new(format!("Invalid UTF-8 in chat template path: {}", e))
                            .unwrap();
                    unsafe {
                        *error = err_msg.into_raw();
                    }
                }
                return ptr::null_mut();
            }
        };
        let tokenizer =
            create_tokenizer_with_chat_template(model_dir_str, Some(chat_template_path_str));
        match tokenizer {
            Ok(tokenizer) => {
                return Box::into_raw(Box::new(tokenizer)).cast();
            }
            Err(e) => {
                if !error.is_null() {
                    let err_msg =
                        CString::new(format!("Failed to create tokenizer: {}", e)).unwrap();
                    unsafe {
                        *error = err_msg.into_raw();
                    }
                }
                return ptr::null_mut();
            }
        }
    }
}

#[no_mangle]
pub extern "C" fn tokenizers_apply_chat_template(
    ptr: *mut libc::c_void,
    messages: *const libc::c_char,
    tools: *const libc::c_char,
    params: *const libc::c_char,
    error: *mut *mut libc::c_char,
) -> *mut libc::c_char {
    if ptr.is_null() {
        if !error.is_null() {
            let err_msg = CString::new("One or more required parameters are null").unwrap();
            unsafe {
                *error = err_msg.into_raw();
            }
        }
        return ptr::null_mut();
    }

    let tokenizer = unsafe { &*(ptr as *mut Arc<dyn traits::Tokenizer>) };
    let hf_tokenizer = tokenizer
        .as_any()
        .downcast_ref::<HuggingFaceTokenizer>()
        .or_else(|| {
            tokenizer
                .as_any()
                .downcast_ref::<LlamaTokenizer>()
                .map(|llama| llama.inner())
        });
    let hf_tokenizer = match hf_tokenizer {
        Some(hf) => hf,
        None => {
            if !error.is_null() {
                let err_msg = CString::new("Invalid tokenizer pointer").unwrap();
                unsafe {
                    *error = err_msg.into_raw();
                }
            }
            return ptr::null_mut();
        }
    };

    let messages_cstr = unsafe { CStr::from_ptr(messages) };
    let messages_str = match messages_cstr.to_str() {
        Ok(s) => s,
        Err(e) => {
            if !error.is_null() {
                let err_msg = CString::new(format!("Invalid UTF-8 in messages: {}", e)).unwrap();
                unsafe {
                    *error = err_msg.into_raw();
                }
            }
            return ptr::null_mut();
        }
    };

    let tools_cstr = unsafe { CStr::from_ptr(tools) };
    let tools_str = match tools_cstr.to_str() {
        Ok(s) => s,
        Err(e) => {
            if !error.is_null() {
                let err_msg = CString::new(format!("Invalid UTF-8 in tools: {}", e)).unwrap();
                unsafe {
                    *error = err_msg.into_raw();
                }
            }
            return ptr::null_mut();
        }
    };
    let params_cstr = unsafe { CStr::from_ptr(params) };
    let params_str = match params_cstr.to_str() {
        Ok(s) => s,
        Err(e) => {
            if !error.is_null() {
                let err_msg = CString::new(format!("Invalid UTF-8 in params: {}", e)).unwrap();
                unsafe {
                    *error = err_msg.into_raw();
                }
            }
            return ptr::null_mut();
        }
    };

    match hf_tokenizer.apply_chat_template(messages_str, tools_str, params_str) {
        Ok(result) => {
            let mut safe_result = result;
            if safe_result.contains('\0') {
                eprintln!("Warning: Result contains NUL characters, which are not allowed in C strings. result len: {}", safe_result.len());
                safe_result = safe_result.replace('\0', "");
            }
            match CString::new(safe_result) {
                Ok(c_string) => c_string.into_raw(),
                Err(e) => {
                    let err_msg = CString::new(format!("CString error: {}", e))
                        .unwrap_or_else(|_| CString::new("Unknown error").unwrap());
                    if !error.is_null() {
                        unsafe {
                            *error = err_msg.into_raw();
                        }
                    }
                    std::ptr::null_mut()
                }
            }
        }
        Err(e) => {
            if !error.is_null() {
                let err_msg =
                    CString::new(format!("Failed to apply chat template: {}", e)).unwrap();
                unsafe {
                    *error = err_msg.into_raw();
                }
            }
            ptr::null_mut()
        }
    }
}

#[no_mangle]
pub extern "C" fn tokenizers_decode(
    ptr: *mut libc::c_void,
    ids: *const u32,
    len: u32,
    skip_special_tokens: bool,
) -> *mut libc::c_char {
    if ptr.is_null() || ids.is_null() {
        return ptr::null_mut();
    }

    let unified_tokenizer = unsafe {
        match ptr.cast::<Arc<dyn traits::Tokenizer>>().as_ref() {
            Some(tokenizer) => tokenizer,
            None => return ptr::null_mut(),
        }
    };
    let ids_slice = unsafe { std::slice::from_raw_parts(ids, len as usize) };

    match unified_tokenizer.decode(ids_slice, skip_special_tokens) {
        Ok(string) => match CString::new(string) {
            Ok(c_string) => c_string.into_raw(),
            Err(_) => ptr::null_mut(),
        },
        Err(_) => ptr::null_mut(),
    }
}

#[no_mangle]
pub extern "C" fn tokenizers_vocab_size(ptr: *mut libc::c_void) -> u32 {
    if ptr.is_null() {
        return 0;
    }

    let tokenizer = unsafe {
        match ptr.cast::<Arc<dyn traits::Tokenizer>>().as_ref() {
            Some(tokenizer) => tokenizer,
            None => return 0,
        }
    };
    tokenizer.vocab_size() as u32
}

#[no_mangle]
pub extern "C" fn tokenizers_model_max_len(ptr: *mut libc::c_void) -> u64 {
    if ptr.is_null() {
        return 0;
    }

    let tokenizer = unsafe {
        match ptr.cast::<Arc<dyn traits::Tokenizer>>().as_ref() {
            Some(tokenizer) => tokenizer,
            None => return 0,
        }
    };
    tokenizer.model_max_length() as u64
}

#[no_mangle]
pub extern "C" fn tokenizers_free_tokenizer(ptr: *mut ::libc::c_void) {
    if ptr.is_null() {
        return;
    }
    unsafe {
        drop(Box::from_raw(ptr.cast::<Arc<dyn traits::Tokenizer>>()));
    }
}

#[no_mangle]
pub extern "C" fn tokenizers_free_string(ptr: *mut libc::c_char) {
    if ptr.is_null() {
        return;
    }
    unsafe {
        drop(CString::from_raw(ptr));
    }
}

// TODO: maybe add tiktoken

#[no_mangle]
pub extern "C" fn tokenizers_encode(
    ptr: *mut libc::c_void,
    message: *const libc::c_char,
    add_special_tokens: boolean_t,
) -> TokenizersBuffer {
    if ptr.is_null() || message.is_null() {
        return TokenizersBuffer {
            ids: ptr::null_mut(),
            len: 0,
        };
    }

    let tokenizer = unsafe {
        match ptr.cast::<Arc<dyn traits::Tokenizer>>().as_ref() {
            Some(tokenizer) => tokenizer.clone(),
            None => {
                return TokenizersBuffer {
                    ids: ptr::null_mut(),
                    len: 0,
                }
            }
        }
    };

    let message_cstr = unsafe { CStr::from_ptr(message) };
    let message_bytes = message_cstr.to_bytes();

    // Use from_utf8_lossy to handle invalid UTF-8 gracefully
    // This will replace invalid sequences with the replacement character (U+FFFD)
    let message_cow = String::from_utf8_lossy(message_bytes);
    let message = message_cow.as_ref();

    let add_special_tokens_bool = add_special_tokens != 0;

    let mut token_ids_vec = match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        tokenizer.encode(message, add_special_tokens_bool)
    })) {
        Ok(Ok(encodings)) => encodings.token_ids().to_vec(),
        Ok(Err(_)) | Err(_) => {
            return TokenizersBuffer {
                ids: ptr::null_mut(),
                len: 0,
            }
        }
    };
    token_ids_vec.shrink_to_fit();
    let len = token_ids_vec.len() as u32;
    let ids_ptr = token_ids_vec.as_mut_ptr();
    std::mem::forget(token_ids_vec);
    TokenizersBuffer {
        ids: ids_ptr,
        len: len as usize,
    }
}

#[no_mangle]
pub extern "C" fn tokenizers_free_buffer(buf: TokenizersBuffer) {
    if !buf.ids.is_null() {
        unsafe {
            Vec::from_raw_parts(buf.ids, buf.len, buf.len);
        }
    }
}
