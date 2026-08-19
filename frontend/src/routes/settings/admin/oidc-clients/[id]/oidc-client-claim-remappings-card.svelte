<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { m } from '$lib/paraglide/messages';
	import type { OidcClient, OidcClientCredentials } from '$lib/types/oidc.type';
	import { preventDefault } from '$lib/utils/event-util';
	import { createForm } from '$lib/utils/form-util';
	import { slide } from 'svelte/transition';
	import { z } from 'zod/v4';
	import ClaimRemappingsInput from '../claim-remappings-input.svelte';

	let {
		client,
		callback
	}: {
		client: OidcClient;
		callback: (credentials: OidcClientCredentials) => Promise<boolean>;
	} = $props();

	let isLoading = $state(false);
	// CIMD clients have their credentials managed externally, so no editing is offered here
	const isCIMDClient = $derived(client.clientType === 'cimd');

	const formSchema = z.object({
		credentials: z.object({
			claimRemappings: z
				.array(
					z.object({
						claimName: z.string().min(1).max(255),
						sourceType: z.enum(['user_field', 'custom_claim', 'static']),
						sourceValue: z.string().min(1).max(1000)
					})
				)
				.default([])
		})
	});

	const { inputs, errors, ...form } = createForm(formSchema, {
		credentials: {
			claimRemappings: client.credentials?.claimRemappings?.map((remap) => ({ ...remap })) ?? []
		}
	});

	const hasRemappings = $derived($inputs.credentials.value.claimRemappings.length > 0);

	function getRemappingErrors(errs: z.ZodError<any> | undefined) {
		return errs?.issues
			.filter((error) =>
				['credentials', 'claimRemappings'].every((segment, index) => error.path[index] === segment)
			)
			.map((error) => ({ ...error, path: error.path.slice(2) }));
	}

	function addRemapping() {
		$inputs.credentials.value.claimRemappings = [
			...$inputs.credentials.value.claimRemappings,
			{ claimName: '', sourceType: 'user_field', sourceValue: '' }
		];
	}

	async function onSubmit() {
		if (isCIMDClient) return;

		const data = form.validate();
		if (!data) return;

		isLoading = true;
		// Preserve credential sub-objects owned by other cards so submitting this card does not wipe them
		// secrets and federatedIdentities are carried over from client.credentials when present, else defaulted empty
		const merged: OidcClientCredentials = {
			...(client.credentials ?? { federatedIdentities: [], secrets: [] }),
			claimRemappings: data.credentials.claimRemappings
		};
		await callback(merged).finally(() => (isLoading = false));
	}
</script>

<form novalidate onsubmit={preventDefault(onSubmit)}>
	<Card.Root data-testid="claim-remappings-card">
		<Card.Header>
			<div class="flex items-center justify-between gap-4">
				<div>
					<Card.Title>{m.claim_remappings()}</Card.Title>
					<Card.Description>
						{m.claim_remappings_description()}
					</Card.Description>
				</div>
				{#if !hasRemappings}
					<Button disabled={isCIMDClient} onclick={addRemapping}>{m.create()}</Button>
				{/if}
			</div>
		</Card.Header>
		{#if hasRemappings}
			<div transition:slide>
				<Card.Content>
					<ClaimRemappingsInput
						bind:claimRemappings={$inputs.credentials.value.claimRemappings}
						errors={getRemappingErrors($errors)}
						disabled={isCIMDClient}
					/>
				</Card.Content>
			</div>
		{/if}
		{#if !isCIMDClient && hasRemappings}
			<Card.Footer class="justify-end">
				<Button type="submit" disabled={isLoading}>{m.save()}</Button>
			</Card.Footer>
		{/if}
	</Card.Root>
</form>
