<script lang="ts">
	import CheckboxWithLabel from '$lib/components/form/checkbox-with-label.svelte';
	import FileInput from '$lib/components/form/file-input.svelte';
	import FormInput from '$lib/components/form/form-input.svelte';
	import { Button } from '$lib/components/ui/button';
	import Label from '$lib/components/ui/label/label.svelte';
	import { m } from '$lib/paraglide/messages';
	import type {
		OidcClient,
		OidcClientCreate,
		OidcClientCreateWithLogo
	} from '$lib/types/oidc.type';
	import { createForm } from '$lib/utils/form-util';
	import { z } from 'zod';
	import OidcCallbackUrlInput from './oidc-callback-url-input.svelte';

	let {
		callback,
		existingClient
	}: {
		existingClient?: OidcClient;
		callback: (user: OidcClientCreateWithLogo) => Promise<boolean>;
	} = $props();

	let isLoading = $state(false);
	let logo = $state<File | null | undefined>();
	let logoDataURL: string | null = $state(
		existingClient?.hasLogo ? `/api/oidc/clients/${existingClient!.id}/logo` : null
	);

	const client: OidcClientCreate = {
		name: existingClient?.name || '',
		callbackURLs: existingClient?.callbackURLs || [],
		logoutCallbackURLs: existingClient?.logoutCallbackURLs || [],
		isPublic: existingClient?.isPublic || false,
		pkceEnabled: existingClient?.pkceEnabled || false
	};

	const formSchema = z.object({
		name: z.string().min(2).max(50),
		callbackURLs: z.array(z.string().nonempty()).default([]),
		logoutCallbackURLs: z.array(z.string().nonempty()),
		isPublic: z.boolean(),
		pkceEnabled: z.boolean()
	});

	type FormSchema = typeof formSchema;
	const { inputs, ...form } = createForm<FormSchema>(formSchema, client);

	async function onSubmit() {
		const data = form.validate();
		if (!data) return;
		isLoading = true;
		const success = await callback({
			...data,
			logo
		});
		// Reset form if client was successfully created
		if (success && !existingClient) form.reset();
		isLoading = false;
	}

	function onLogoChange(e: Event) {
		const file = (e.target as HTMLInputElement).files?.[0] || null;
		if (file) {
			logo = file;
			const reader = new FileReader();
			reader.onload = (event) => {
				logoDataURL = event.target?.result as string;
			};
			reader.readAsDataURL(file);
		}
	}

	function resetLogo() {
		logo = null;
		logoDataURL = null;
	}
</script>

<form onsubmit={onSubmit}>
	<div class="grid grid-cols-1 gap-x-3 gap-y-7 sm:flex-row md:grid-cols-2">
		<FormInput label={m.name()} class="w-full" bind:input={$inputs.name} />
		<div></div>
		<OidcCallbackUrlInput
			label={m.callback_urls()}
			description={m.callback_url_description()}
			class="w-full"
			bind:callbackURLs={$inputs.callbackURLs.value}
			bind:error={$inputs.callbackURLs.error}
		/>
		<OidcCallbackUrlInput
			label={m.logout_callback_urls()}
			description={m.logout_callback_url_description()}
			class="w-full"
			bind:callbackURLs={$inputs.logoutCallbackURLs.value}
			bind:error={$inputs.logoutCallbackURLs.error}
		/>
		<CheckboxWithLabel
			id="public-client"
			label={m.public_client()}
			description={m.public_clients_description()}
			bind:checked={$inputs.isPublic.value}
		/>
		<CheckboxWithLabel
			id="pkce"
			label={m.pkce()}
			description={m.public_key_code_exchange_is_a_security_feature_to_prevent_csrf_and_authorization_code_interception_attacks()}
			bind:checked={$inputs.pkceEnabled.value}
		/>
	</div>
	<div class="mt-8">
		<Label for="logo">{m.logo()}</Label>
		<div class="mt-2 flex items-end gap-3">
			{#if logoDataURL}
				<div class="bg-muted size-32 rounded-2xl p-3">
					<img
						class="m-auto max-h-full max-w-full object-contain"
						src={logoDataURL}
						alt={m.name_logo({ name: $inputs.name.value })}
					/>
				</div>
			{/if}
			<div class="flex flex-col gap-2">
				<FileInput
					id="logo"
					variant="secondary"
					accept="image/png, image/jpeg, image/svg+xml"
					onchange={onLogoChange}
				>
					<Button variant="secondary">
						{logoDataURL ? m.change_logo() : m.upload_logo()}
					</Button>
				</FileInput>
				{#if logoDataURL}
					<Button variant="outline" onclick={resetLogo}>{m.remove_logo()}</Button>
				{/if}
			</div>
		</div>
	</div>
	<div class="w-full"></div>
	<div class="mt-5 flex justify-end">
		<Button {isLoading} type="submit">{m.save()}</Button>
	</div>
</form>
