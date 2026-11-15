<#-- Data Fabric branded email layout (#618). Email clients strip <style>
     blocks, so everything is inline. The nested content comes from the base
     per-event templates (verification, password reset, ...) rendered with the
     zh overrides in messages/messages_en.properties. -->
<#macro emailLayout>
<html lang="${locale.language}" dir="${(ltr)?then('ltr','rtl')}">
<body style="margin:0;padding:24px 12px;background:#f0eee6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Hiragino Sans GB','Microsoft YaHei',sans-serif;color:#1f1d1a;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;margin:0 auto;">
    <tr>
      <td style="padding:0 8px 16px;font-size:16px;font-weight:600;">
        <span style="color:#c96442;font-size:13px;">&#9670;</span>
        <span style="color:#1f1d1a;">Data Fabric</span>
      </td>
    </tr>
    <tr>
      <td style="background:#ffffff;border:1px solid #e0dcd0;border-radius:14px;padding:28px 28px 24px;font-size:15px;line-height:1.7;">
        <#nested>
      </td>
    </tr>
    <tr>
      <td style="padding:16px 8px 0;font-size:12.5px;color:#6b6862;text-align:center;">
        此邮件由 Data Fabric 服务发送，请勿直接回复。
      </td>
    </tr>
  </table>
</body>
</html>
</#macro>
