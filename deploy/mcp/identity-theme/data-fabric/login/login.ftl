<#import "template.ftl" as layout>
<#-- Data Fabric themed login page (#618). Keeps the contract the self-service
     acceptance script relies on: form id "kc-form-login", POST to
     url.loginAction, the reset-credentials and registrations links (#603). -->
<@layout.registrationLayout displayMessage=!messagesPerField.existsError('username','password') displayInfo=realm.password && realm.registrationAllowed && !registrationDisabled??; section>
    <#if section = "header">
        登录 Data Fabric
    <#elseif section = "form">
        <div id="kc-form">
          <div id="kc-form-wrapper">
            <#if realm.password>
                <form id="kc-form-login" onsubmit="login.disabled = true; return true;" action="${url.loginAction}" method="post">
                    <#if !usernameHidden??>
                        <div class="${properties.kcFormGroupClass!}">
                            <label for="username" class="${properties.kcLabelClass!}">用户名或邮箱</label>
                            <input tabindex="2" id="username" class="${properties.kcInputClass!}" name="username" value="${(login.username!'')}" type="text"
                                   autofocus autocomplete="username"
                                   aria-invalid="<#if messagesPerField.existsError('username','password')>true</#if>"
                                   dir="ltr"
                            />
                            <#if messagesPerField.existsError('username','password')>
                                <span id="input-error" class="${properties.kcInputErrorMessageClass!}" aria-live="polite">
                                        ${kcSanitize(messagesPerField.getFirstError('username','password'))?no_esc}
                                </span>
                            </#if>
                        </div>
                    </#if>

                    <div class="${properties.kcFormGroupClass!}">
                        <label for="password" class="${properties.kcLabelClass!}">密码</label>
                        <input tabindex="3" id="password" class="${properties.kcInputClass!}" name="password" type="password" autocomplete="current-password"
                               aria-invalid="<#if messagesPerField.existsError('username','password')>true</#if>"
                               dir="ltr"
                        />
                        <#if usernameHidden?? && messagesPerField.existsError('username','password')>
                            <span id="input-error" class="${properties.kcInputErrorMessageClass!}" aria-live="polite">
                                    ${kcSanitize(messagesPerField.getFirstError('username','password'))?no_esc}
                            </span>
                        </#if>
                    </div>

                    <#if realm.resetPasswordAllowed>
                        <div class="${properties.kcFormOptionsWrapperClass!}">
                            <span><a tabindex="6" href="${url.loginResetCredentialsUrl}">忘记密码？</a></span>
                        </div>
                    </#if>

                    <div id="kc-form-buttons" class="${properties.kcFormGroupClass!}">
                        <input type="hidden" id="id-hidden-input" name="credentialId" <#if auth.selectedCredential?has_content>value="${auth.selectedCredential}"</#if>/>
                        <input tabindex="7" class="${properties.kcButtonClass!} ${properties.kcButtonPrimaryClass!} ${properties.kcButtonBlockClass!}" name="login" id="kc-login" type="submit" value="登录"/>
                    </div>
                </form>
            </#if>
          </div>
        </div>
    <#elseif section = "info" >
        <#if realm.password && realm.registrationAllowed && !registrationDisabled??>
            <div id="kc-registration-container">
                <div id="kc-registration">
                    <span>还没有账号？ <a tabindex="8" href="${url.registrationUrl}">注册</a></span>
                </div>
            </div>
        </#if>
    </#if>
</@layout.registrationLayout>
