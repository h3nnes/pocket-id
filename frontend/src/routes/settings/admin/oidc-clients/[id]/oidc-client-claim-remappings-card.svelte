<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.ts';
	import * as Card from '#lib/components/ui/card/index.ts';
	import { m } from '#lib/paraglide/messages.js';
	import type { OidcClient, OidcClientClaimRemapping } from '#lib/types/oidc.type.ts';
	import { createForm } from '#lib/utils/form-util.ts';
	import { trackFormChanges } from '#lib/utils/unsaved-changes-util.svelte.ts';
	import { slide } from 'svelte/transition';
	import { z } from 'zod/v4';
	import ClaimRemappingsInput from '../claim-remappings-input.svelte';

	let {
		client,
		callback
	}: {
		client: OidcClient;
		callback: (claimRemappings: OidcClientClaimRemapping[]) => Promise<void>;
	} = $props();

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

	const formStore = createForm(formSchema, {
		credentials: {
			claimRemappings: client.credentials?.claimRemappings?.map((remap) => ({ ...remap })) ?? []
		}
	});
	const { inputs, errors } = formStore;

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

	// Metadata document clients manage their own credentials, so there is nothing to save here
	if (!isCIMDClient) {
		trackFormChanges(
			() => formStore,
			(data) => callback(data.credentials.claimRemappings)
		);
	}
</script>

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
</Card.Root>
