<#import "template.ftl" as layout>
<#-- Data Fabric themed registration page (#618): only the fields a signup
     actually requires. With registrationEmailAsUsername enabled the email
     doubles as the username (one field, posted as "username"); otherwise
     username and email render separately. firstName and lastName are not
     rendered here; the realm user profile must also stop requiring them
     (see README). Form id "kc-register-form" and POST to
     url.registrationAction keep the acceptance script's contract (#603). -->
<@layout.registrationLayout displayMessage=messagesPerField.exists('global') displayRequiredFields=false; section>
    <#if section = "header">
        注册 Data Fabric
    <#elseif section = "form">
        <form id="kc-register-form" class="${properties.kcFormClass!}" action="${url.registrationAction}" method="post">
            <#if realm.registrationEmailAsUsername>
                <#-- Email doubles as the username: one field, posted as "email";
                     Keycloak syncs it to the username server-side (this mirrors
                     what the base theme renders in this mode). -->
                <div class="${properties.kcFormGroupClass!}">
                    <label for="email" class="${properties.kcLabelClass!}">邮箱（将作为用户名）</label>
                    <input type="email" id="email" class="${properties.kcInputClass!}" name="email" autocomplete="username"
                           autofocus
                           aria-invalid="<#if messagesPerField.existsError('email')>true</#if>"
                           dir="ltr"
                    />
                    <#if messagesPerField.existsError('email')>
                        <span id="input-error-email" class="${properties.kcInputErrorMessageClass!}" aria-live="polite">
                                ${kcSanitize(messagesPerField.get('email'))?no_esc}
                        </span>
                    </#if>
                </div>
            <#else>
                <div class="${properties.kcFormGroupClass!}">
                    <label for="username" class="${properties.kcLabelClass!}">用户名</label>
                    <input type="text" id="username" class="${properties.kcInputClass!}" name="username" autocomplete="username"
                           autofocus
                           aria-invalid="<#if messagesPerField.existsError('username')>true</#if>"
                           dir="ltr"
                    />
                    <#if messagesPerField.existsError('username')>
                        <span id="input-error-username" class="${properties.kcInputErrorMessageClass!}" aria-live="polite">
                                ${kcSanitize(messagesPerField.get('username'))?no_esc}
                        </span>
                    </#if>
                </div>

                <div class="${properties.kcFormGroupClass!}">
                    <label for="email" class="${properties.kcLabelClass!}">邮箱</label>
                    <input type="email" id="email" class="${properties.kcInputClass!}" name="email" autocomplete="email"
                           aria-invalid="<#if messagesPerField.existsError('email')>true</#if>"
                           dir="ltr"
                    />
                    <#if messagesPerField.existsError('email')>
                        <span id="input-error-email" class="${properties.kcInputErrorMessageClass!}" aria-live="polite">
                                ${kcSanitize(messagesPerField.get('email'))?no_esc}
                        </span>
                    </#if>
                </div>
            </#if>

            <div class="${properties.kcFormGroupClass!}">
                <label for="password" class="${properties.kcLabelClass!}">密码</label>
                <input type="password" id="password" class="${properties.kcInputClass!}" name="password"
                       autocomplete="new-password"
                       aria-invalid="<#if messagesPerField.existsError('password','password-confirm')>true</#if>"
                       dir="ltr"
                />
                <#if messagesPerField.existsError('password')>
                    <span id="input-error-password" class="${properties.kcInputErrorMessageClass!}" aria-live="polite">
                            ${kcSanitize(messagesPerField.get('password'))?no_esc}
                    </span>
                </#if>
            </div>

            <div class="${properties.kcFormGroupClass!}">
                <label for="password-confirm" class="${properties.kcLabelClass!}">确认密码</label>
                <input type="password" id="password-confirm" class="${properties.kcInputClass!}" name="password-confirm"
                       autocomplete="new-password"
                       aria-invalid="<#if messagesPerField.existsError('password-confirm')>true</#if>"
                       dir="ltr"
                />
                <#if messagesPerField.existsError('password-confirm')>
                    <span id="input-error-password-confirm" class="${properties.kcInputErrorMessageClass!}" aria-live="polite">
                            ${kcSanitize(messagesPerField.get('password-confirm'))?no_esc}
                    </span>
                </#if>
            </div>

            <div class="${properties.kcFormGroupClass!}">
                <div id="kc-form-options" class="${properties.kcFormOptionsWrapperClass!}">
                    <span><a href="${url.loginUrl}">返回登录</a></span>
                </div>

                <div id="kc-form-buttons" class="${properties.kcFormGroupClass!}">
                    <input class="${properties.kcButtonClass!} ${properties.kcButtonPrimaryClass!} ${properties.kcButtonBlockClass!}" type="submit" value="注册"/>
                </div>
            </div>
        </form>
    </#if>
</@layout.registrationLayout>
