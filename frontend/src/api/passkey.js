import { apiClient } from './client';
function requirePasskeySupport() {
    if (!window.PublicKeyCredential || !navigator.credentials) {
        throw new Error('Passkeys are not supported by this browser');
    }
}
function base64URLToBuffer(value) {
    const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
    const padded = normalized + '='.repeat((4 - (normalized.length % 4)) % 4);
    const binary = atob(padded);
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    return bytes.buffer;
}
function bufferToBase64URL(value) {
    if (value === null)
        return null;
    const bytes = new Uint8Array(value);
    let binary = '';
    for (const byte of bytes)
        binary += String.fromCharCode(byte);
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}
function creationOptionsFromJSON(value) {
    const options = { ...value };
    options.challenge = base64URLToBuffer(String(options.challenge));
    const user = { ...options.user };
    user.id = base64URLToBuffer(String(user.id));
    options.user = user;
    if (Array.isArray(options.excludeCredentials)) {
        options.excludeCredentials = options.excludeCredentials.map((descriptor) => ({
            ...descriptor,
            id: base64URLToBuffer(String(descriptor.id))
        }));
    }
    return options;
}
function requestOptionsFromJSON(value) {
    const options = { ...value };
    options.challenge = base64URLToBuffer(String(options.challenge));
    if (Array.isArray(options.allowCredentials)) {
        options.allowCredentials = options.allowCredentials.map((descriptor) => ({
            ...descriptor,
            id: base64URLToBuffer(String(descriptor.id))
        }));
    }
    return options;
}
function serializeRegistrationCredential(credential) {
    const response = credential.response;
    return {
        id: credential.id,
        rawId: bufferToBase64URL(credential.rawId),
        type: credential.type,
        authenticatorAttachment: credential.authenticatorAttachment,
        clientExtensionResults: credential.getClientExtensionResults(),
        response: {
            attestationObject: bufferToBase64URL(response.attestationObject),
            clientDataJSON: bufferToBase64URL(response.clientDataJSON),
            transports: typeof response.getTransports === 'function' ? response.getTransports() : []
        }
    };
}
function serializeAssertionCredential(credential) {
    const response = credential.response;
    return {
        id: credential.id,
        rawId: bufferToBase64URL(credential.rawId),
        type: credential.type,
        authenticatorAttachment: credential.authenticatorAttachment,
        clientExtensionResults: credential.getClientExtensionResults(),
        response: {
            authenticatorData: bufferToBase64URL(response.authenticatorData),
            clientDataJSON: bufferToBase64URL(response.clientDataJSON),
            signature: bufferToBase64URL(response.signature),
            userHandle: bufferToBase64URL(response.userHandle)
        }
    };
}
async function login(proof) {
    requirePasskeySupport();
    const { data: begin } = proof
        ? await apiClient.post('/auth/passkey/login/begin', proof)
        : await apiClient.post('/auth/passkey/login/begin');
    const credential = await navigator.credentials.get({
        publicKey: requestOptionsFromJSON(begin.options.publicKey)
    });
    if (!(credential instanceof PublicKeyCredential)) {
        throw new Error('Passkey sign-in was cancelled');
    }
    const { data } = await apiClient.post('/auth/passkey/login/finish', {
        session_token: begin.session_token,
        credential: serializeAssertionCredential(credential)
    });
    return data;
}
async function register(name, password) {
    requirePasskeySupport();
    const { data: begin } = await apiClient.post('/user/passkeys/register/begin', { password });
    const credential = await navigator.credentials.create({
        publicKey: creationOptionsFromJSON(begin.options.publicKey)
    });
    if (!(credential instanceof PublicKeyCredential)) {
        throw new Error('Passkey creation was cancelled');
    }
    const { data } = await apiClient.post('/user/passkeys/register/finish', {
        session_token: begin.session_token,
        name,
        credential: serializeRegistrationCredential(credential)
    });
    return data;
}
async function list() {
    const { data } = await apiClient.get('/user/passkeys');
    return data;
}
async function rename(id, name) {
    await apiClient.patch(`/user/passkeys/${id}`, { name });
}
async function remove(id, password) {
    await apiClient.delete(`/user/passkeys/${id}`, { data: { password } });
}
export const passkeyAPI = {
    isSupported: () => Boolean(window.PublicKeyCredential && navigator.credentials),
    login,
    register,
    list,
    rename,
    remove
};
