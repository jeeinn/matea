import { ElMessage } from 'element-plus'

/**
 * Copy text to clipboard. Works on both HTTPS and HTTP (including the
 * local-network / reverse-proxy HTTP setup many users start with).
 *
 * navigator.clipboard is only available in secure contexts (HTTPS/localhost),
 * so we fall back to the legacy document.execCommand('copy') trick on plain
 * HTTP hosts.
 */
export async function copyToClipboard(text, successMsg = '已复制到剪贴板') {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      ElMessage.success(successMsg)
      return true
    } catch {
      // Fall through to legacy method.
    }
  }

  // Legacy fallback for non-secure contexts.
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  textarea.setAttribute('readonly', '')
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  try {
    const ok = document.execCommand('copy')
    if (ok) {
      ElMessage.success(successMsg)
      return true
    }
  } catch (e) {
    // ignore
  } finally {
    document.body.removeChild(textarea)
  }
  ElMessage.error('复制失败，请手动选择文本复制')
  return false
}
